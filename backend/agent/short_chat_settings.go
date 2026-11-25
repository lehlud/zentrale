package agent

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type ShortChatSettings struct {
	MachineID string `json:"machineId"`
	PoolID    string `json:"poolId"`
}

func (settings ShortChatSettings) enabled() bool {
	return settings.MachineID != "" || settings.PoolID != ""
}

func (engine *Engine) ShortChatSettings() (ShortChatSettings, error) {
	var settings ShortChatSettings
	encoded, err := engine.Store.Setting("short_chat_execution")
	if err != nil || encoded == "" {
		return settings, err
	}
	return settings, json.Unmarshal([]byte(encoded), &settings)
}

func (engine *Engine) SaveShortChatSettings(settings ShortChatSettings) (ShortChatSettings, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

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
