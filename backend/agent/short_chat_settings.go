package agent

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type ShortChatSettings struct {
	MachineID      string `json:"machineId"`
	PoolID         string `json:"poolId"`
	Local          bool   `json:"local"`
	RetentionDays  int    `json:"retentionDays"`
	VMLifetimeDays int    `json:"vmLifetimeDays"`
}

const (
	defaultShortChatRetentionDays  = 45
	defaultShortChatVMLifetimeDays = 7
	maxShortChatDays               = 3650
)

func (settings ShortChatSettings) withDefaults() ShortChatSettings {
	if settings.RetentionDays == 0 {
		settings.RetentionDays = defaultShortChatRetentionDays
	}
	if settings.VMLifetimeDays == 0 {
		settings.VMLifetimeDays = defaultShortChatVMLifetimeDays
	}
	return settings
}

func (settings ShortChatSettings) enabled() bool {
	return settings.Local || settings.MachineID != "" || settings.PoolID != ""
}

func (settings ShortChatSettings) executionTarget() (machineID, poolID string) {
	if settings.Local {
		return remote.LocalShortChatMachineID, ""
	}
	return settings.MachineID, settings.PoolID
}

func (engine *Engine) ShortChatSettings() (ShortChatSettings, error) {
	var settings ShortChatSettings
	encoded, err := engine.Store.Setting("short_chat_execution")
	if err != nil || encoded == "" {
		return settings.withDefaults(), err
	}

	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		return settings, err
	}
	settings = settings.withDefaults()
	return settings, settings.validateDays()
}

func (settings ShortChatSettings) validateDays() error {
	if settings.RetentionDays < 1 || settings.RetentionDays > maxShortChatDays ||
		settings.VMLifetimeDays < 1 || settings.VMLifetimeDays > maxShortChatDays {
		return errors.New("short-chat retention and VM lifetime must be between 1 and 3650 days")
	}
	return nil
}

func (engine *Engine) SaveShortChatSettings(settings ShortChatSettings) (ShortChatSettings, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	settings = settings.withDefaults()
	if err := settings.validateDays(); err != nil {
		return settings, err
	}
	if settings.Local && (settings.MachineID != "" || settings.PoolID != "") {
		return settings, errors.New("select one short-chat execution target")
	}
	if settings.Local && !remote.LocalShortChatAvailable() {
		return settings, errors.New("local short-chat VMs require usable /dev/kvm and the managed QEMU runner")
	}

	if (settings.MachineID != "" && settings.PoolID != "") || strings.Contains(settings.MachineID, "/") {
		return settings, errors.New("select one machine or one pool")
	}
	validate := func(id string) error {
		if err := engine.Remote.Connector.RequireWorkTarget(id); err != nil {
			return err
		}
		machine, err := engine.Remote.Connector.Machine(id)
		if err != nil {
			return err
		}
		_, err = remote.ShortChatMachine(machine)
		return err
	}
	if settings.MachineID != "" {
		if err := validate(settings.MachineID); err != nil {
			return settings, err
		}
	}
	if settings.PoolID != "" {
		var pool domain.Pool
		if err := engine.Store.Load("pool", settings.PoolID, &pool); err != nil {
			return settings, err
		}
		eligible := 0
		for _, machine := range pool.Machines {
			if machine.JumpHostOnly {
				continue
			}
			if err := validate(pool.ID + "/" + machine.ID); err != nil {
				return settings, err
			}
			eligible++
		}
		if eligible == 0 {
			return settings, errors.New("the pool has no short-chat execution target")
		}
	}

	return settings, engine.Store.SetSetting("short_chat_execution", JSON(settings))
}
