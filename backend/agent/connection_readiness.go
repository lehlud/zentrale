package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type connectionReadiness struct {
	key        string
	generation string
	status     domain.SandboxStatus
}

// Private process-local digests only. Credentials never enter events/status.
// Conservatively invalidate on key and trust rotations, including in-place.
func (engine *Engine) sourceReadinessKey(project domain.Project, sources []domain.Source) (string, error) {
	sources = append([]domain.Source(nil), sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	bindings := []any{}
	accounts := map[string]any{}
	for _, source := range sources {
		if source.BindingID == "" {
			continue
		}
		var binding map[string]any
		if err := engine.Store.Load("repository_binding", source.BindingID, &binding); err != nil {
			return "", err
		}
		bindings = append(bindings, binding)
		if id := stringArg(binding, "connectionId"); id != "" {
			var account map[string]any
			if err := engine.Store.Load("git_account", id, &account); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			accounts[id] = account
		}
	}
	keys, err := engine.Store.ListPrivSSHKeys()
	if err != nil {
		return "", err
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	trusted, err := engine.Store.TrustedKeys()
	if err != nil {
		return "", err
	}
	sort.Slice(trusted, func(i, j int) bool { return bytes.Compare(trusted[i], trusted[j]) < 0 })
	identity, err := engine.Store.Setting("commit_identity")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(JSON([]any{project.ID, project.Directory, sources, bindings, accounts, keys, trusted, identity, project.CommitName, project.CommitEmail})))), nil
}

func (engine *Engine) connectionReadinessKey(record domain.Agent, machine domain.Machine, project domain.Project) (string, error) {
	owner, err := engine.vmOwner(record.ID)
	if err != nil {
		return "", err
	}
	rows, err := engine.listAll("source", &project.ID)
	if err != nil {
		return "", err
	}
	var sources []domain.Source
	if err := decode(JSON(rows), &sources); err != nil {
		return "", err
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	sourceKey, err := engine.sourceReadinessKey(project, sources)
	if err != nil {
		return "", err
	}
	configured, err := engine.Remote.Connector.Machine(machine.ID)
	if err != nil {
		return "", err
	}
	entry := engine.connectionEntry(record.ID)
	if entry == nil {
		return "", errors.New("engine closed")
	}
	entry.mutex.Lock()
	prepared := entry.sourcesKey == sourceKey
	entry.mutex.Unlock()
	if !prepared {
		return "", errors.New("source preparation fingerprint changed")
	}
	storage, err := engine.listAll("project_storage", &project.ID)
	if err != nil {
		return "", err
	}
	jumps := []domain.Machine{}
	pending := append([]string(nil), configured.Jumps...)
	seen := map[string]bool{machine.ID: true}
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		jump, err := engine.Remote.Connector.Machine(id)
		if err != nil {
			return "", err
		}
		jumps = append(jumps, jump)
		pending = append(pending, jump.Jumps...)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(JSON([]any{owner.ID, owner.ProjectID, owner.MachineID, record.ProjectID, project, configured, jumps, machine.StorageMounts, storage, sourceKey})))), nil
}

func (engine *Engine) probeReadyGeneration(ctx context.Context, record domain.Agent, connection *remote.Connection, machine domain.Machine, project domain.Project, expectedBootID ...string) (string, error) {
	owner, err := engine.vmOwner(record.ID)
	if err != nil {
		return "", err
	}
	operation := engine.vmOperation(owner.ID, machine.ID)
	if err := operation.Lock(ctx); err != nil {
		return "", err
	}
	defer operation.Unlock()
	if operation.stopped {
		return "", errors.New("VM scope was stopped")
	}
	probeContext, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	identity := map[string]string{"agentId": owner.ID, "projectId": project.ID, "workspace": project.Directory, "temporaryDirectory": agentTemporaryDirectory(owner.ID)}
	command := "python3 -I -c " + remote.Quote(connectionReadinessProbe)
	if len(expectedBootID) > 0 {
		command += " " + remote.Quote(expectedBootID[0])
	}
	output, err := remote.RunPipe(probeContext, connection.Client, command, strings.NewReader(JSON(identity)))
	if err != nil {
		return "", err
	}
	generation := strings.TrimSpace(output)
	if len(generation) != 64 || strings.Trim(generation, "0123456789abcdef") != "" {
		return "", errors.New("invalid readiness generation")
	}
	return generation, nil
}

func (engine *Engine) reuseReadyConnection(ctx context.Context, record domain.Agent, connection *remote.Connection, machine domain.Machine, project domain.Project) (domain.SandboxStatus, bool) {
	entry := engine.connectionEntry(record.ID)
	if entry == nil {
		return domain.SandboxStatus{}, false
	}
	entry.mutex.Lock()
	previous := entry.ready
	entry.mutex.Unlock()
	if previous == nil {
		return domain.SandboxStatus{}, false
	}
	key, err := engine.connectionReadinessKey(record, machine, project)
	if err == nil && key == previous.key {
		generation, failure := engine.probeReadyGeneration(ctx, record, connection, machine, project)
		if failure == nil && generation == previous.generation {
			currentKey, failure := engine.connectionReadinessKey(record, machine, project)
			entry.mutex.Lock()
			valid := failure == nil && currentKey == key && entry.ready == previous && entry.connection != nil && entry.connection.Client == connection.Client
			entry.mutex.Unlock()
			if valid {
				return previous.status, true
			}
		}
	}
	// Changed generation/credentials also require fresh repository credentials.
	entry.mutex.Lock()
	entry.ready, entry.sourcesKey = nil, ""
	entry.mutex.Unlock()
	// A changed project/storage/jump/credential scope must also re-resolve host
	// placement and storage, rather than carrying stale prepared mount objects.
	engine.releaseConnection(record.ID)
	return domain.SandboxStatus{}, false
}

func (engine *Engine) rememberReadyConnection(ctx context.Context, record domain.Agent, connection *remote.Connection, machine domain.Machine, project domain.Project, status domain.SandboxStatus, preparedBootID string) {
	if status.Level != remote.SandboxMicroVM || machine.MicroVMCommand != "" || preparedBootID == "" {
		return
	}
	key, err := engine.connectionReadinessKey(record, machine, project)
	if err != nil {
		return
	}
	generation, err := engine.probeReadyGeneration(ctx, record, connection, machine, project, preparedBootID)
	if err != nil {
		return
	} // Optional optimization; never weaken a cold check.
	currentKey, err := engine.connectionReadinessKey(record, machine, project)
	if err != nil || currentKey != key {
		return
	}
	entry := engine.connectionEntry(record.ID)
	if entry == nil {
		return
	}
	entry.mutex.Lock()
	defer entry.mutex.Unlock()
	if entry.connection != nil && entry.connection.Client == connection.Client && entry.sourcesKey != "" {
		entry.ready = &connectionReadiness{key: key, generation: generation, status: status}
	}
}

// Reuse native bootId/version fingerprints and the runner's nonce/heartbeat
// protocol without running bootstrap, prerequisite probes or command staging.
const connectionReadinessProbe = `import fcntl,hashlib,json,os,re,secrets,stat,sys,time
from pathlib import Path
p=json.load(sys.stdin)
agent,project=p['agentId'],p['projectId']
assert all(isinstance(v,str) and re.fullmatch(r'[A-Za-z0-9_.-]{1,80}',v) for v in (agent,project))
root=Path('/var/tmp')/('zentrale-microvm-'+str(os.getuid()))
scope=root/'agents'/hashlib.sha256((project+'\0'+agent).encode()).hexdigest()
control=scope/'session'
for directory in (root,root/'agents',scope,control):
 s=directory.lstat()
 assert stat.S_ISDIR(s.st_mode) and s.st_uid==os.getuid() and not s.st_mode&0o077
def read(directory,name):
 fd=os.open(str(directory/name),os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
 with os.fdopen(fd,'rb') as f:
  assert stat.S_ISREG(os.fstat(f.fileno()).st_mode)
  data=f.read(65537)
  assert len(data)<=65536
  return data
lock=os.open(str(scope/'lock'),os.O_RDONLY|os.O_NOFOLLOW)
temporary=None
try:
 fcntl.flock(lock,fcntl.LOCK_EX)
 assert json.loads(read(scope,'identity.json'))==p
 assert not (control/'stopped').exists() and not (control/'stop').exists()
 pid=int(read(control,'supervisor.pid'))
 os.kill(pid,0)
 proof=json.loads(read(control,'ready.json'))
 assert re.fullmatch(r'[A-Za-z0-9-]{16,80}',proof.get('bootId',''))
 if len(sys.argv)>1: assert proof['bootId']==sys.argv[1]
 version=read(control,'version').decode()
 request=read(control,'request.json').decode()
 generation=hashlib.sha256(json.dumps([proof['bootId'],version,request,pid],sort_keys=True).encode()).hexdigest()
 nonce=secrets.token_hex(16)
 temporary=control/('.health-'+nonce)
 fd=os.open(str(temporary),os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'w') as f: json.dump({'nonce':nonce},f)
 os.replace(temporary,control/'health.request')
 initial=(control/'heartbeat').stat().st_mtime_ns
 deadline=time.monotonic()+5
 while True:
  try: matched=json.loads(read(control,'health.response')).get('nonce')==nonce
  except (FileNotFoundError,ValueError): matched=False
  if matched or (control/'heartbeat').stat().st_mtime_ns!=initial: break
  if time.monotonic()>=deadline: raise RuntimeError('guest dispatcher is unresponsive')
  time.sleep(0.025)
 assert not (control/'stopped').exists() and not (control/'stop').exists()
 os.kill(pid,0)
 assert json.loads(read(control,'ready.json')).get('bootId')==proof['bootId']
 assert read(control,'version').decode()==version and read(control,'request.json').decode()==request
 assert int(read(control,'supervisor.pid'))==pid
 print(generation)
finally:
 try:
  if temporary is not None: temporary.unlink()
 except FileNotFoundError: pass
 os.close(lock)
`
