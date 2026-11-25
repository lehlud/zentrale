package agent

import (
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/gitforge"
	"github.com/lehlud/zentrale/huggingface"
)

func (engine *Engine) GitFor(userID string) *gitforge.Service {
	if userID == "" || userID == domain.InitialUserID {
		return engine.GitForge
	}
	if service, ok := engine.personalGit.Load(userID); ok {
		return service.(*gitforge.Service)
	}
	service, _ := engine.personalGit.LoadOrStore(userID, engine.GitForge.ForUser(userID))
	return service.(*gitforge.Service)
}

func (engine *Engine) HubFor(userID string) *huggingface.Service {
	if userID == "" || userID == domain.InitialUserID {
		return engine.HuggingFace
	}
	if service, ok := engine.personalHub.Load(userID); ok {
		return service.(*huggingface.Service)
	}
	service, _ := engine.personalHub.LoadOrStore(userID, engine.HuggingFace.ForUser(userID))
	return service.(*huggingface.Service)
}

func (engine *Engine) conversationOwner(record domain.Agent) (string, error) {
	if record.ChatOwnerID != "" {
		return record.ChatOwnerID, nil
	}
	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		return "", err
	}
	return project.Owner(), nil
}
