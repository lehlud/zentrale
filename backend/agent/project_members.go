package agent

import (
	"errors"
	"slices"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) SetProjectMember(projectID, ownerID, memberID string, add bool) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	var project domain.Project
	if err := engine.Store.Load("project", projectID, &project); err != nil || project.Owner() != ownerID {
		return errors.New("project owner required")
	}
	if project.Archived {
		return errors.New("project is archived")
	}
	if err := engine.projectAvailable(projectID); err != nil {
		return err
	}
	if memberID == ownerID {
		return errors.New("the project owner cannot be added or removed as a member")
	}

	if add {
		account, err := engine.Store.UserAccount(memberID)
		if err != nil || account.Disabled {
			return errors.New("active user not found")
		}
		if !slices.Contains(project.MemberIDs, memberID) {
			project.MemberIDs = append(project.MemberIDs, memberID)
		}
	} else {
		project.MemberIDs = slices.DeleteFunc(project.MemberIDs, func(id string) bool { return id == memberID })
	}

	if err := engine.Store.Patch("project", project.ID, map[string]any{"memberIds": project.MemberIDs}); err != nil {
		return err
	}
	engine.Emit("resources", "", nil)
	return nil
}
