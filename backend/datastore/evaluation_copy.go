package datastore

import "errors"

// PrepareEvaluationCopy is for a private, disposable database copy before an
// engine has opened it. A benchmark must not reattach the original service's
// processes, resume its agents, or consume its pending cleanup/login actions.
func (i *Instance) PrepareEvaluationCopy() error {
	if i == nil || i.Closed() {
		return errClosed
	}
	prepared := false
	i.identityOnce.Do(func() {
		prepared = true
		tx, err := i.db.Begin()
		if err != nil {
			i.identityError = err
			return
		}
		defer tx.Rollback()
		for _, query := range []string{
			`DELETE FROM settings WHERE name='installation-id'`,
			`DELETE FROM credential_records WHERE name='installation/daemon-token' OR name LIKE 'job-runtime/%' OR name LIKE 'job-input/%' OR name LIKE 'editor-runtime/%' OR name LIKE 'terminal-runtime/%'`,
			`DELETE FROM resources WHERE kind IN ('agent','agent_input','job','job_output','job_completion','microvm','vm_cleanup','editor_attachment','terminal_attachment','agent_run','goal','question','mcp_server','tool_activity','message','agent_stop','job_stop','provider_login','git_login','huggingface_login')`,
			`DELETE FROM sessions`,
		} {
			if _, err := tx.Exec(query); err != nil {
				i.identityError = err
				return
			}
		}
		if err := tx.Commit(); err != nil {
			i.identityError = err
			return
		}
		i.identityID, i.identityToken, i.identityError = i.initializeInstallationIdentity()
	})
	if !prepared {
		return errors.New("evaluation isolation must be prepared before this database's installation identity is used")
	}
	return i.identityError
}
