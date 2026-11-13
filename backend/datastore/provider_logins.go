package datastore

import "errors"

// ListPendingProviderLogins returns only bounded IDs, never credential blobs.
// Only the latest session per provider is eligible: older pending grants must
// not replace a newer completed or cancelled login after a service restart.
func (i *Instance) ListPendingProviderLogins(limit int) ([]string, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	if limit < 1 || limit > 32 {
		return nil, errors.New("login recovery limit must be between 1 and 32")
	}
	rows, err := i.readDB().Query(`SELECT id FROM (
		SELECT id,data,created_at,row_number() OVER (PARTITION BY parent_id ORDER BY created_at DESC,id DESC) AS login_rank
		FROM resources WHERE kind='provider_login'
	) WHERE login_rank=1 AND json_extract_string(data,'$.session.status')='pending'
	ORDER BY created_at,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
