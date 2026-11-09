package remote

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type editorSession struct {
	project domain.Project
	target  EditorTarget
	runtime editorRuntime
	info    domain.EditorSession

	ready      chan struct{}
	done       chan struct{}
	cancel     context.CancelFunc
	connection *Connection
	socketPath string
	err        error

	activeRequests int
	lastUsed       time.Time
}

// Editors supervises one SSH remote agent per project. Workspace services use
// its machine and pool lease; the web workbench is hosted by Zentrale.
type Editors struct {
	remote *Manager
	ctx    context.Context
	cancel context.CancelFunc

	mutex    sync.Mutex
	sessions map[string]*editorSession
	workers  sync.WaitGroup
}

func NewEditors(remote *Manager) *Editors {
	ctx, cancel := context.WithCancel(context.Background())
	editors := &Editors{
		remote:   remote,
		ctx:      ctx,
		cancel:   cancel,
		sessions: make(map[string]*editorSession),
	}

	editors.workers.Go(editors.expireIdleSessions)

	return editors
}

func (editors *Editors) Close() {
	editors.mutex.Lock()
	editors.cancel()
	editors.mutex.Unlock()
	editors.workers.Wait()
}

func (editors *Editors) Start(ctx context.Context, project domain.Project) (domain.EditorSession, error) {
	return editors.StartFor(ctx, project, EditorTarget{})
}

func (editors *Editors) StartFor(
	ctx context.Context,
	project domain.Project,
	target EditorTarget,
) (domain.EditorSession, error) {
	if !ValidID(project.ID) {
		return domain.EditorSession{}, errors.New("invalid project id")
	}

	if !project.ManagedWorkspace {
		if _, err := Workspace(project.Directory, ""); err != nil {
			return domain.EditorSession{}, err
		}
	}

	editors.mutex.Lock()

	if editors.ctx.Err() != nil {
		editors.mutex.Unlock()
		return domain.EditorSession{}, errors.New("editor service is closed")
	}

	key := project.ID + ":" + target.AgentID
	session := editors.sessions[key]
	previous := session

	if session != nil {
		select {
		case <-session.done:
			session.cancel()
			session = nil
		default:
		}
	}

	if session != nil && (!sameEditorProject(session.project, project) || session.target != target) {
		session.cancel()
		session = nil
	}

	if session == nil {
		sessionContext, cancel := context.WithCancel(editors.ctx)
		id := rand.Text()
		session = &editorSession{
			project: project,
			target:  target,
			info:    domain.EditorSession{ID: id, ProjectID: project.ID, AgentID: target.AgentID},
			ready:   make(chan struct{}),
			done:    make(chan struct{}),
			cancel:  cancel,
		}

		editors.sessions[key] = session
		editors.workers.Go(func() {
			// A previous configuration must release its pool lease before its replacement starts.
			if previous != nil {
				<-previous.done
			}

			editors.run(sessionContext, session)
		})
	}

	session.lastUsed = time.Now()
	editors.mutex.Unlock()

	select {
	case <-ctx.Done():
		return domain.EditorSession{}, ctx.Err()
	case <-session.ready:
		return session.info, session.err
	}
}

func (editors *Editors) run(ctx context.Context, session *editorSession) {
	defer close(session.done)
	defer editors.remote.Release("ide:" + session.info.ID)

	var readyOnce sync.Once
	ready := func(err error) {
		readyOnce.Do(func() {
			session.err = err
			close(session.ready)
		})
	}
	defer func() { ready(errors.New("remote editor stopped before becoming ready")) }()

	timer := time.AfterFunc(5*time.Minute, session.cancel)
	defer timer.Stop()

	excluded := map[string]bool{}

	if session.target.MachineID != "" {
		candidates, err := editors.remote.projectCandidates(session.project)
		if err != nil {
			ready(err)
			return
		}

		for _, candidate := range candidates {
			excluded[candidate.id] = candidate.id != session.target.MachineID
		}
	}

	connection, machine, err := editors.remote.Connect(ctx, session.project, "ide:"+session.info.ID, session.target.MachineID, excluded)
	if err != nil {
		ready(err)
		return
	}
	defer connection.Close()

	resolved, err := editors.remote.ResolveProject(ctx, connection, machine, session.project)
	if err == nil {
		machine.StorageMounts, err = editors.remote.PrepareStorage(ctx, connection, machine, resolved)
	}

	if err != nil {
		ready(err)
		return
	}

	editors.mutex.Lock()
	session.project = resolved
	session.info.Directory = resolved.Directory
	editors.mutex.Unlock()
	session.connection = connection
	session.info.MachineID, session.info.MachineName = machine.ID, machine.Name
	prefix := "/ide/" + session.project.ID + "/" + session.info.ID
	session.info.URL = prefix + "/?folder=" + url.QueryEscape(session.project.Directory)

	if session.target.AgentID != "" {
		session.info.URL += "&agent=" + url.QueryEscape(session.target.AgentID)
	}

	session.runtime, err = prepareEditorRuntime(ctx, connection, machine, session.project, session.target, prefix)
	if err != nil {
		ready(err)
		return
	}

	session.info.Sandbox = session.runtime.sandbox
	err = runEditorProcess(ctx, connection.Client, session.runtime.command, func(output string) {
		select {
		case <-session.ready:
			return
		default:
		}

		socketPath := editorOutputSocket(output)
		if socketPath == "" {
			return
		}

		readyOnce.Do(func() {
			session.socketPath = socketPath
			timer.Stop()
			close(session.ready)
		})
	})

	ready(err)
}

// Info remains available after a tunnel failure, so Zentrale can keep serving
// its local workbench while VS Code presents its reconnection UI.
func (editors *Editors) Info(project domain.Project, sessionID string) (domain.EditorSession, error) {
	editors.mutex.Lock()
	defer editors.mutex.Unlock()

	session := editors.findSession(project.ID, sessionID)
	if session == nil || session.info.ID != sessionID || !sameEditorProject(session.project, project) {
		return domain.EditorSession{}, errors.New("editor session expired; reconnect the editor")
	}

	select {
	case <-session.ready:
	default:
		return domain.EditorSession{}, errors.New("editor is still starting")
	}

	if session.err != nil {
		return domain.EditorSession{}, session.err
	}

	session.lastUsed = time.Now()

	return session.info, nil
}

func (editors *Editors) Current(project domain.Project) domain.EditorSession {
	return editors.CurrentFor(project, "")
}

func (editors *Editors) CurrentFor(project domain.Project, agentID string) domain.EditorSession {
	editors.mutex.Lock()
	defer editors.mutex.Unlock()

	session := editors.sessions[project.ID+":"+agentID]
	if session == nil || !sameEditorProject(session.project, project) {
		return domain.EditorSession{}
	}

	select {
	case <-session.ready:
	default:
		return domain.EditorSession{}
	}

	select {
	case <-session.done:
		return domain.EditorSession{}
	default:
	}

	if session.err != nil {
		return domain.EditorSession{}
	}

	return session.info
}

// Transport pins remote-agent requests to their SSH session. Static workbench
// assets never use this transport or fall back to a remote web server.
func (editors *Editors) Transport(project domain.Project, sessionID string) (*http.Transport, func(), error) {
	editors.mutex.Lock()
	defer editors.mutex.Unlock()

	session := editors.findSession(project.ID, sessionID)
	if session == nil || session.info.ID != sessionID || !sameEditorProject(session.project, project) {
		return nil, nil, errors.New("editor session expired; reconnect the editor")
	}

	select {
	case <-session.ready:
	default:
		return nil, nil, errors.New("editor is still starting")
	}

	if session.err != nil {
		return nil, nil, session.err
	}

	select {
	case <-session.done:
		return nil, nil, errors.New("editor disconnected; reconnect the editor")
	default:
	}

	session.activeRequests++
	session.lastUsed = time.Now()
	release := func() {
		editors.mutex.Lock()
		session.activeRequests--
		session.lastUsed = time.Now()
		editors.mutex.Unlock()
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialEditor(ctx, session)
		},
		ResponseHeaderTimeout: 5 * time.Minute,
	}

	return transport, release, nil
}

func (editors *Editors) Stop(projectID, sessionID string) error {
	editors.mutex.Lock()
	session := editors.findSession(projectID, sessionID)
	if session != nil && session.info.ID == sessionID {
		delete(editors.sessions, projectID+":"+session.target.AgentID)
		session.cancel()
	} else {
		session = nil
	}

	editors.mutex.Unlock()

	if session != nil {
		<-session.done
	}

	return nil
}

func (editors *Editors) expireIdleSessions() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-editors.ctx.Done():
			return
		case now := <-ticker.C:
			editors.mutex.Lock()

			for id, session := range editors.sessions {
				if session.activeRequests == 0 && now.Sub(session.lastUsed) > 15*time.Minute {
					delete(editors.sessions, id)
					session.cancel()
				}
			}

			editors.mutex.Unlock()
		}
	}
}

func sameEditorProject(first, second domain.Project) bool {
	return first.ID == second.ID && first.ManagedWorkspace == second.ManagedWorkspace &&
		(first.ManagedWorkspace || first.Directory == second.Directory) &&
		first.CommitName == second.CommitName && first.CommitEmail == second.CommitEmail &&
		reflect.DeepEqual(first.MachineIDs, second.MachineIDs) &&
		reflect.DeepEqual(first.PoolIDs, second.PoolIDs)
}

func editorOutputSocket(output string) string {
	const marker = "ZENTRALE_EDITOR_READY "

	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}

		var details struct {
			SocketPath string `json:"socketPath"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &details) != nil {
			continue
		}

		directory := path.Dir(details.SocketPath)
		if path.Dir(directory) == "/tmp" &&
			strings.HasPrefix(path.Base(directory), "ze-") &&
			path.Base(details.SocketPath) == "vscode.sock" {
			return details.SocketPath
		}
	}

	return ""
}

func (editors *Editors) StopProject(projectID string) error {
	return editors.stopMatching(func(session *editorSession) bool {
		return session.project.ID == projectID
	})
}

func (editors *Editors) StopAgent(agentID string) error {
	return editors.stopMatching(func(session *editorSession) bool {
		return session.target.AgentID == agentID
	})
}

// Called with mutex held. Session IDs identify tabs independently of which
// agent is currently selected in the project UI.
func (editors *Editors) findSession(projectID, sessionID string) *editorSession {
	for _, session := range editors.sessions {
		if session.project.ID == projectID && session.info.ID == sessionID {
			return session
		}
	}

	return nil
}

func (editors *Editors) stopMatching(matches func(*editorSession) bool) error {
	editors.mutex.Lock()
	var sessions []*editorSession

	for key, session := range editors.sessions {
		if matches(session) {
			delete(editors.sessions, key)
			session.cancel()
			sessions = append(sessions, session)
		}
	}

	editors.mutex.Unlock()

	for _, session := range sessions {
		<-session.done
	}

	return nil
}
