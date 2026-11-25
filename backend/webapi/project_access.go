package webapi

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/graphql-go/graphql"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/domain"
)

func (server *Server) saveOwnedProject(ctx context.Context, value domain.Project) (domain.Project, error) {
	user := state(ctx).user
	var previous domain.Project
	loadErr := sql.ErrNoRows
	if value.ID != "" {
		loadErr = server.Store.Load("project", value.ID, &previous)
	}

	switch {
	case loadErr == nil:
		if err := server.projectAccess(user, value.ID, true, true); err != nil {
			return value, err
		}
		value.OwnerID = previous.Owner()
	case errors.Is(loadErr, sql.ErrNoRows):
		if !user.IsAdmin() && !user.CanCreateProjects {
			return value, errors.New("project creation is not enabled for this account")
		}
		value.OwnerID = user.ID
	default:
		return value, loadErr
	}
	if user.ID != domain.InitialUserID && (value.RepositorySSHURL != "" || value.RepositorySSHKeyID != 0) {
		return value, errors.New("use a personal Git connection instead of a global SSH key")
	}
	if user.ID != domain.InitialUserID && !value.ManagedWorkspace {
		return value, errors.New("use a managed workspace for personal projects")
	}

	return server.Engine.SaveProjectContext(ctx, value)
}

func (server *Server) projectAgentAccess(user domain.User, id string, write bool) error {
	var record domain.Agent
	if err := server.Store.Load("agent", id, &record); err != nil || record.ProjectID == "" || record.ChatOwnerID != "" {
		return errProjectUnavailable
	}
	return server.projectAccess(user, record.ProjectID, write, false)
}

func (server *Server) protectProjectFields(queries, mutations graphql.Fields) {
	for _, name := range []string{"conversationBootstrap", "sources", "agents", "editorSession", "projectMove"} {
		server.projectField(queries[name], "projectId", false, false, false)
	}
	server.projectField(queries["project"], "id", false, false, false)
	server.projectField(queries["agent"], "id", true, false, false)
	server.agentResourceField(queries["job"], "job", false)
	server.agentResourceField(mutations["stopJob"], "job", true)
	server.agentResourceField(mutations["answerQuestion"], "question", true)

	for _, name := range []string{"goal", "goalHistory", "goalReviews", "teamwork"} {
		guardField(queries[name], "agentId", func(user domain.User, id string) error {
			return server.projectAgentAccess(user, id, false)
		})
	}
	guardField(mutations["cancelGoal"], "agentId", func(user domain.User, id string) error {
		return server.projectAgentAccess(user, id, true)
	})

	for _, name := range []string{
		"agentInputs", "agentRunState", "agentRemovalBusy", "messages", "notes", "questions",
		"approvals", "usage", "jobs", "inferenceTraces", "usageSummary", "recentMessages", "conversationModels",
	} {
		server.projectField(queries[name], "agentId", true, false, false)
	}
	for _, name := range []string{"createAgent", "startEditor", "stopEditor"} {
		server.projectField(mutations[name], "projectId", false, true, false)
	}
	for _, name := range []string{"moveProject", "resetProjectVM", "connectRepository"} {
		server.projectField(mutations[name], "projectId", false, true, true)
	}
	for _, name := range []string{
		"prompt", "submitInput", "cancelInput", "resumeInputs", "stopAgent", "renameAgent", "prepareAgent", "setConversationModel",
	} {
		server.projectField(mutations[name], "agentId", true, true, false)
	}
}

func (server *Server) agentResourceField(field *graphql.Field, kind string, write bool) {
	resolve := field.Resolve
	field.Resolve = authenticated(func(params graphql.ResolveParams) (any, error) {
		var record struct {
			AgentID string `json:"agentId"`
		}
		id, _ := params.Args["id"].(string)
		if err := server.Store.Load(kind, id, &record); err != nil {
			return nil, errProjectUnavailable
		}

		if err := server.agentAccess(state(params.Context).user, record.AgentID, write); err != nil {
			return nil, err
		}
		return resolve(params)
	})
}

var (
	errProjectUnavailable = errors.New("project not found or not available")
	errProjectArchived    = errors.New("project is archived")
	errProjectOwner       = errors.New("project owner required")
)

func canSeeProject(user domain.User, project domain.Project) bool {
	return user.ID != "" && (user.ID == project.Owner() || slices.Contains(project.MemberIDs, user.ID))
}

// projectAccess keeps project viewing, changing settings, and starting remote
// work distinct. An application administrator is not automatically a member.
func (server *Server) projectAccess(user domain.User, id string, write, ownerOnly bool) error {
	var project domain.Project
	if err := server.Store.Load("project", id, &project); err != nil || !canSeeProject(user, project) {
		return errProjectUnavailable
	}
	if ownerOnly && project.Owner() != user.ID {
		return errProjectOwner
	}
	if write && project.Archived {
		return errProjectArchived
	}
	return nil
}

func (server *Server) agentAccess(user domain.User, id string, write bool) error {
	var agent domain.Agent
	if err := server.Store.Load("agent", id, &agent); err != nil {
		return errProjectUnavailable
	}
	if agent.ChatOwnerID != "" {
		if !ownsShortChat(user, agent) {
			return errProjectUnavailable
		}
		return nil
	}
	return server.projectAccess(user, agent.ProjectID, write, false)
}

func ownsShortChat(user domain.User, chat domain.Agent) bool {
	return user.ID != "" && user.ID == chat.ChatOwnerID && chat.ProjectID == "" && chat.Role == "main" && chat.ParentID == ""
}

func (server *Server) projectURLAccess(request echo.Context, parameter string, agentID, write bool) error {
	user, ok := server.Auth.User(server.token(request.Request()))
	if !ok {
		return echo.ErrUnauthorized
	}
	id := request.Param(parameter)
	var err error
	if agentID {
		err = server.agentAccess(user, id, write)
	} else {
		err = server.projectAccess(user, id, write, false)
	}
	if err != nil {
		return echo.ErrNotFound
	}
	return nil
}

func (server *Server) projectURL(parameter string, agentID, write bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(request echo.Context) error {
			if err := server.projectURLAccess(request, parameter, agentID, write); err != nil {
				return err
			}
			return next(request)
		}
	}
}

func (server *Server) projectField(field *graphql.Field, argument string, agentID, write, ownerOnly bool) {
	guardField(field, argument, func(user domain.User, id string) error {
		if agentID {
			return server.agentAccess(user, id, write)
		}
		return server.projectAccess(user, id, write, ownerOnly)
	})
}

func guardField(field *graphql.Field, argument string, access func(domain.User, string) error) {
	resolve := field.Resolve
	field.Resolve = authenticated(func(params graphql.ResolveParams) (any, error) {
		user := state(params.Context).user
		id, _ := params.Args[argument].(string)
		if err := access(user, id); err != nil {
			return nil, err
		}

		return resolve(params)
	})
}
