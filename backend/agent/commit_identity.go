package agent

import "github.com/lehlud/zentrale/domain"

func (engine *Engine) CommitIdentity() (domain.CommitIdentity, error) {
	return engine.UserCommitIdentity(domain.InitialUserID)
}

func commitIdentityKey(userID string) string {
	if userID == domain.InitialUserID {
		return "commit_identity"
	}
	return "commit_identity:" + userID
}

func (engine *Engine) UserCommitIdentity(userID string) (domain.CommitIdentity, error) {
	encoded, err := engine.Store.Setting(commitIdentityKey(userID))
	if err != nil || encoded == "" {
		return domain.CommitIdentity{}, err
	}

	var identity domain.CommitIdentity
	if err := decode(encoded, &identity); err != nil {
		return identity, err
	}

	return domain.NormalizeCommitIdentity(identity)
}

func (engine *Engine) SaveCommitIdentity(identity domain.CommitIdentity) error {
	return engine.SaveUserCommitIdentity(domain.InitialUserID, identity)
}

func (engine *Engine) SaveUserCommitIdentity(userID string, identity domain.CommitIdentity) error {
	identity, err := domain.NormalizeCommitIdentity(identity)
	if err != nil {
		return err
	}

	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	if err := engine.Store.SetSetting(commitIdentityKey(userID), JSON(identity)); err != nil {
		return err
	}

	engine.Emit("commit_identity", "", nil)

	return nil
}

func (engine *Engine) ProjectCommitIdentity(project domain.Project) (domain.CommitIdentity, error) {
	if project.CommitName != "" || project.CommitEmail != "" {
		return domain.NormalizeCommitIdentity(domain.CommitIdentity{
			Name:  project.CommitName,
			Email: project.CommitEmail,
		})
	}

	return engine.UserCommitIdentity(project.Owner())
}
