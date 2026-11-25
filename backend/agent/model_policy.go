package agent

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

type ModelPolicy struct {
	Restricted bool     `json:"restricted"`
	Models     []string `json:"models"`
}

func (engine *Engine) ModelPolicy(providerID string) (ModelPolicy, error) {
	policy := ModelPolicy{Models: []string{}}
	err := engine.Store.Load("model_policy", providerID, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return policy, err
}

func (engine *Engine) SaveModelPolicy(providerID string, policy ModelPolicy) (ModelPolicy, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	var provider domain.Provider
	if err := engine.Store.Load("provider", providerID, &provider); err != nil {
		return policy, err
	}

	if len(policy.Models) > 1000 {
		return policy, errors.New("select at most 1000 models")
	}

	models := make([]string, 0, len(policy.Models))
	if policy.Restricted {
		for _, id := range policy.Models {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			var model domain.LanguageModel
			if err := engine.Store.Load("model", modelResourceID(providerID, id), &model); err != nil {
				return policy, fmt.Errorf("model %q is not in the provider catalog", id)
			}
			models = append(models, id)
		}
	}
	slices.Sort(models)
	policy.Models = slices.Compact(models)
	if err := engine.Store.Save("model_policy", providerID, providerID, policy); err != nil {
		return policy, err
	}
	engine.Emit("resources", "", nil)
	return policy, nil
}

func (engine *Engine) requireApprovedModel(providerID, modelID string) error {
	policy, err := engine.ModelPolicy(providerID)
	if err != nil {
		return err
	}
	if policy.Restricted && !slices.Contains(policy.Models, modelID) {
		return errors.New("this model is not enabled by an administrator")
	}
	return nil
}
