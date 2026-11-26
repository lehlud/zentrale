package agent

import (
	"errors"
	"slices"

	"github.com/lehlud/zentrale/domain"
)

const (
	ChatModels    = "chat"
	ProjectModels = "project"
)

type ModelPresetPolicy struct {
	Configured bool                 `json:"configured"`
	Presets    []domain.ModelChoice `json:"presets"`
	Defaults   domain.ModelDefaults `json:"defaults"`
}

func modelScope(record domain.Agent) string {
	if record.ChatOwnerID != "" {
		return ChatModels
	}
	return ProjectModels
}

func validModelScope(scope string) bool {
	return scope == ChatModels || scope == ProjectModels
}

func (engine *Engine) ModelPresetPolicy(scope string) (ModelPresetPolicy, error) {
	policy, err := engine.storedModelPresetPolicy(scope)
	if err == nil && !policy.Configured {
		policy.Defaults, err = engine.ModelDefaults()
	}
	return policy, err
}

func (engine *Engine) storedModelPresetPolicy(scope string) (ModelPresetPolicy, error) {
	policy := ModelPresetPolicy{Presets: []domain.ModelChoice{}}
	if !validModelScope(scope) {
		return policy, errors.New("invalid model scope")
	}
	encoded, err := engine.Store.Setting("model_presets:" + scope)
	if err != nil || encoded == "" {
		return policy, err
	}
	err = decode(encoded, &policy)
	return policy, err
}

func (engine *Engine) SaveModelPresetPolicy(scope string, policy ModelPresetPolicy) error {
	if !validModelScope(scope) {
		return errors.New("invalid model scope")
	}
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if len(policy.Presets) > 1000 {
		return errors.New("select at most 1000 model presets")
	}
	unique := make([]domain.ModelChoice, 0, len(policy.Presets))
	resolved := make(map[domain.ModelChoice]domain.ModelChoice, len(policy.Presets))
	for _, choice := range policy.Presets {
		if _, err := engine.validateModelChoice(choice); err != nil {
			return err
		}
		normalized, err := engine.normalizeReasoning(choice, "main")
		if err != nil {
			return err
		}
		resolved[choice] = normalized
		if !slices.Contains(unique, normalized) {
			unique = append(unique, normalized)
		}
	}
	if choice, ok := resolved[policy.Defaults.Main]; ok {
		policy.Defaults.Main = choice
	}
	if choice, ok := resolved[policy.Defaults.Approver]; ok {
		policy.Defaults.Approver = choice
	}
	policy.Configured, policy.Presets = true, unique
	if err := validatePresetDefaults(policy, policy.Defaults); err != nil {
		return err
	}
	if err := engine.Store.SetSetting("model_presets:"+scope, JSON(policy)); err != nil {
		return err
	}
	engine.Emit("model_defaults", "", nil)
	return nil
}

func validatePresetDefaults(policy ModelPresetPolicy, defaults domain.ModelDefaults) error {
	for _, choice := range []domain.ModelChoice{defaults.Main, defaults.Approver} {
		if choice != (domain.ModelChoice{}) && !slices.Contains(policy.Presets, choice) {
			return errors.New("a default must be one of the released model presets")
		}
	}
	return nil
}

func personalModelKey(scope, userID string) string { return "model_defaults:" + scope + ":" + userID }

func (engine *Engine) PersonalModelDefaults(scope, userID string) (domain.ModelDefaults, error) {
	var defaults domain.ModelDefaults
	if !validModelScope(scope) {
		return defaults, errors.New("invalid model scope")
	}
	encoded, err := engine.Store.Setting(personalModelKey(scope, userID))
	if err != nil || encoded == "" {
		return defaults, err
	}
	err = decode(encoded, &defaults)
	return defaults, err
}

func (engine *Engine) SavePersonalModelDefaults(scope, userID string, defaults domain.ModelDefaults) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	owner, err := engine.Store.UserAccount(userID)
	if err != nil || owner.Disabled || !owner.IsAdmin() {
		return errors.New("administrator required")
	}
	policy, err := engine.ModelPresetPolicy(scope)
	if err != nil {
		return err
	}
	if err := validatePresetDefaults(policy, defaults); err != nil {
		return err
	}
	if err := engine.Store.SetSetting(personalModelKey(scope, userID), JSON(defaults)); err != nil {
		return err
	}
	engine.Emit("model_defaults", "", nil)
	return nil
}

func (engine *Engine) EffectiveModelDefaults(scope, userID string) (domain.ModelDefaults, error) {
	policy, err := engine.ModelPresetPolicy(scope)
	if err != nil || !policy.Configured || userID == "" {
		return policy.Defaults, err
	}
	owner, err := engine.Store.UserAccount(userID)
	if err != nil || !owner.IsAdmin() || owner.Disabled {
		return policy.Defaults, nil
	}
	personal, err := engine.PersonalModelDefaults(scope, userID)
	if err != nil {
		return policy.Defaults, err
	}
	usable := func(choice domain.ModelChoice) bool {
		if !slices.Contains(policy.Presets, choice) {
			return false
		}
		_, err := engine.validateModelChoice(choice)
		return err == nil
	}
	if usable(personal.Main) {
		policy.Defaults.Main = personal.Main
	}
	if usable(personal.Approver) {
		policy.Defaults.Approver = personal.Approver
	}
	return policy.Defaults, nil
}

func (engine *Engine) requireModelPreset(scope string, choice domain.ModelChoice) error {
	policy, err := engine.storedModelPresetPolicy(scope)
	if err != nil {
		return err
	}
	if policy.Configured && !slices.Contains(policy.Presets, choice) {
		return errors.New("this model and thinking level are not enabled for this type of conversation")
	}
	return nil
}

func (engine *Engine) resolveScopedChoice(scope, userID string, choice domain.ModelChoice, role string) (domain.ModelChoice, error) {
	policy, err := engine.storedModelPresetPolicy(scope)
	if err != nil {
		return choice, err
	}
	if !policy.Configured {
		return engine.resolveChoice(choice, role)
	}
	if choice == (domain.ModelChoice{}) {
		defaults, err := engine.EffectiveModelDefaults(scope, userID)
		if err != nil {
			return choice, err
		}
		choice = defaults.Main
		if role == "approver" {
			choice = defaults.Approver
		}
		if choice == (domain.ModelChoice{}) {
			return choice, errors.New("an administrator must set a default model for this type of conversation")
		}
	}
	if _, err := engine.validateModelChoice(choice); err != nil {
		return choice, err
	}
	if err := engine.requireModelPreset(scope, choice); err != nil {
		return choice, err
	}
	normalized, err := engine.normalizeReasoning(choice, role)
	if err != nil || normalized != choice {
		return choice, errors.New("the released thinking level is no longer supported by this model")
	}
	return choice, nil
}

func (engine *Engine) recordModelChoice(record domain.Agent) (domain.ModelChoice, error) {
	ownerID := record.ChatOwnerID
	return engine.resolveScopedChoice(modelScope(record), ownerID, agentModelChoice(record), record.Role)
}
