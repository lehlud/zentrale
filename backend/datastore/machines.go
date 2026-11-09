package datastore

import (
	"database/sql"

	"github.com/lehlud/zentrale/domain"
)

func (i *Instance) ListMachines() ([]domain.Machine, error) {
	rows, err := i.List("machine", nil, 500, 0)
	if err != nil {
		return nil, err
	}

	return decodeResult[[]domain.Machine](rows)
}

func (i *Instance) GetMachine(id string) (*domain.Machine, error) {
	var m domain.Machine
	if err := i.Load("machine", id, &m); err != nil {
		return nil, err
	}

	return &m, nil
}

func (i *Instance) AddMachine(m domain.Machine) error {
	var existing domain.Machine
	err := i.Load("machine", m.ID, &existing)
	if err == nil {
		return sql.ErrNoRows
	}

	if err != sql.ErrNoRows {
		return err
	}

	return i.Save("machine", m.ID, "", m)
}

func (i *Instance) AlterMachine(m domain.Machine) error {
	if _, err := i.GetMachine(m.ID); err != nil {
		return err
	}

	return i.Save("machine", m.ID, "", m)
}

func (i *Instance) DropMachine(id string) error {
	return i.Remove("machine", id)
}
