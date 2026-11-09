package agent

import "github.com/lehlud/zentrale/domain"

func (engine *Engine) CommitIdentity() (domain.CommitIdentity, error) {
	encoded, err := engine.Store.Setting("commit_identity")
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
	identity, err := domain.NormalizeCommitIdentity(identity)
	if err != nil {
		return err
	}
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if err := engine.Store.SetSetting("commit_identity", JSON(identity)); err != nil {
		return err
	}
	engine.Emit("commit_identity", "", identity)
	return nil
}

func (engine *Engine) ProjectCommitIdentity(project domain.Project) (domain.CommitIdentity, error) {
	if project.CommitName != "" || project.CommitEmail != "" {
		return domain.NormalizeCommitIdentity(domain.CommitIdentity{Name: project.CommitName, Email: project.CommitEmail})
	}
	return engine.CommitIdentity()
}
