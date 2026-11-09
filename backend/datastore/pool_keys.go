package datastore

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lehlud/zentrale/domain"
)

// SaveManagedPool commits the pool and its automatically allocated SSH identity
// together. Callers supply topology, never a key identifier. Existing identities
// remain stable when members are added, removed or reordered.
func (instance *Instance) SaveManagedPool(pool domain.Pool) (domain.Pool, error) {
	if instance == nil || instance.Closed() {
		return pool, errClosed
	}
	if pool.KeyID != 0 {
		return pool, errors.New("pool SSH keys are managed automatically")
	}
	for _, machine := range pool.Machines {
		if machine.KeyID != 0 {
			return pool, errors.New("pool SSH keys are managed automatically")
		}
	}
	pool.Machines = append([]domain.Machine(nil), pool.Machines...)
	tx, err := instance.db.Begin()
	if err != nil {
		return pool, err
	}
	defer tx.Rollback()
	var previous domain.Pool
	var data string
	err = tx.QueryRow(`SELECT data::VARCHAR FROM resources WHERE kind='pool' AND id=?`, pool.ID).Scan(&data)
	if err == nil {
		if err := json.Unmarshal([]byte(data), &previous); err != nil {
			return pool, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return pool, err
	}
	usages, err := keyUsages(tx)
	if err != nil {
		return pool, err
	}
	keyID := previous.KeyID
	if keyID == 0 && len(previous.Machines) > 0 {
		return pool, errors.New("pool has no managed SSH identity; unsupported development configuration")
	}
	for _, machine := range previous.Machines {
		if machine.KeyID != keyID {
			return pool, errors.New("pool contains a separate member SSH identity; unsupported development configuration")
		}
	}
	if keyID > 0 {
		var purpose, owner string
		if err := tx.QueryRow(`SELECT purpose,purpose_id FROM private_ssh_keys WHERE id=?`, keyID).Scan(&purpose, &owner); err != nil {
			return pool, err
		}
		if purpose != "pool" || owner != pool.ID {
			return pool, errors.New("pool SSH key is assigned to another purpose")
		}
		for _, usage := range usages[keyID] {
			if usage.Kind != "pool" || usage.ID != pool.ID {
				return pool, errors.New("pool SSH key is assigned to another purpose")
			}
		}
	} else {
		encoded, err := generateSSHKeyData()
		if err != nil {
			return pool, err
		}
		if err := tx.QueryRow(`INSERT INTO private_ssh_keys(x509_data,purpose,purpose_id) VALUES (?,'pool',?) RETURNING id`, encoded, pool.ID).Scan(&keyID); err != nil {
			return pool, err
		}
	}
	pool.KeyID = keyID
	for index := range pool.Machines {
		pool.Machines[index].KeyID = keyID
	}
	encoded, err := json.Marshal(pool)
	if err != nil {
		return pool, err
	}
	if _, err := tx.Exec(`INSERT INTO resources(kind,id,parent_id,data) VALUES ('pool',?,'',?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`, pool.ID, string(encoded)); err != nil {
		return pool, err
	}
	return pool, tx.Commit()
}
