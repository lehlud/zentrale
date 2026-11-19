package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/hosted"
)

const modalDeploying = "deploying"

type modalDeployment struct{ cancel context.CancelFunc }

func (engine *Engine) SetupModalHumanizer(tokenID, tokenSecret, model string) (domain.Provider, error) {
	suffix := "jialin"
	switch model {
	case "", hosted.ModalHumanizer:
		model = hosted.ModalHumanizer
	case hosted.ModalSmaug:
		suffix = "smaug"
	default:
		return domain.Provider{}, errors.New("choose a supported Modal Humanizer model")
	}
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if err := engine.ctx.Err(); err != nil {
		return domain.Provider{}, err
	}
	id := "modal-humanizer-" + suffix
	var previous domain.Provider
	if err := engine.loadProvider(id, &previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Provider{}, err
	}
	if previous.ID != "" && previous.ModalModelID != model {
		return domain.Provider{}, errors.New("this provider ID is already in use")
	}
	tokenID, tokenSecret = strings.TrimSpace(tokenID), strings.TrimSpace(tokenSecret)
	credential := previous.Credential
	if tokenID != "" || tokenSecret != "" {
		credential = tokenID + "." + tokenSecret
	}
	if credential == "" {
		return domain.Provider{}, errors.New("enter both parts of the Modal API token")
	}
	if previous.CatalogStatus == modalDeploying && engine.modalDeployments[id] != nil {
		if previous.Credential != credential {
			return domain.Provider{}, errors.New("this Modal Humanizer is already being deployed")
		}
		previous.Credential = ""
		return previous, nil
	}
	provider := domain.Provider{
		ID: id, Name: "Modal · " + model, Kind: "modal", AuthMode: "api", BillingMode: "metered",
		Credential: credential, ModalModelID: model, CatalogStatus: modalDeploying,
	}
	// Use normal native-connection validation before retaining any credentials.
	provider.BaseURL = "modal://main/" + engine.modalAppName(id) + "/run_batch"
	if err := hosted.ValidateProvider(&provider); err != nil {
		return domain.Provider{}, err
	}
	provider.BaseURL = ""
	if err := engine.saveProvider(provider); err != nil {
		return domain.Provider{}, err
	}
	engine.startModalDeployment(provider)
	engine.Emit("provider", "", map[string]string{"id": id})
	provider.Credential = ""
	return provider, nil
}

func (engine *Engine) modalAppName(providerID string) string {
	scope := sha256.Sum256([]byte(engine.installationID))
	return fmt.Sprintf("zentrale-%x-%s", scope[:8], strings.TrimPrefix(providerID, "modal-"))
}

// Called with configMu held. A deployment uses a stable App name so resuming
// after an interruption updates the same App without starting GPU inference.
func (engine *Engine) startModalDeployment(provider domain.Provider) {
	ctx, cancel := context.WithTimeout(engine.ctx, 25*time.Minute)
	run := &modalDeployment{cancel: cancel}
	engine.modalDeployments[provider.ID] = run
	engine.workers.Go(func() {
		defer cancel()
		destination, err := engine.deployModal(ctx, provider.Credential, engine.modalAppName(provider.ID), provider.ModalModelID)
		engine.configMu.Lock()
		defer engine.configMu.Unlock()
		if engine.modalDeployments[provider.ID] != run {
			return
		}
		delete(engine.modalDeployments, provider.ID)
		if engine.ctx.Err() != nil {
			return // On restart pick up the pending setup.
		}
		status := map[string]any{"catalogStatus": "external", "catalogError": "", "connected": true, "baseUrl": destination}
		if err != nil {
			status = map[string]any{"catalogStatus": "deployment_failed", "catalogError": err.Error(), "connected": false}
		}
		if err := engine.Store.Patch("provider", provider.ID, status); err != nil {
			slog.Warn("save Modal deployment status", "provider", provider.ID, "error", err)
			return
		}
		engine.Emit("provider", "", map[string]string{"id": provider.ID})
		if err == nil {
			configured, failure := engine.Store.Setting("humanizer")
			if failure == nil && configured == "" {
				settings := domain.HumanizerSettings{Hosted: domain.HostedHumanizer{ProviderID: provider.ID, ModelID: provider.ModalModelID}}
				failure = engine.Store.SetSetting("humanizer", JSON(settings))
				if failure == nil {
					engine.Emit("humanizer", "", nil)
				}
			}
			if failure != nil {
				slog.Warn("select deployed Modal Humanizer", "error", failure)
			}
		}
	})
}

func (engine *Engine) recoverModalDeployments() error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	rows, err := engine.listAll("provider", nil, "id", "catalogStatus", "modalModelId")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if stringArg(row, "catalogStatus") != modalDeploying || stringArg(row, "modalModelId") == "" {
			continue
		}
		var provider domain.Provider
		if err := engine.loadProvider(stringArg(row, "id"), &provider); err != nil {
			return err
		}
		engine.startModalDeployment(provider)
	}
	return nil
}

func (engine *Engine) cancelModalDeployment(id string) {
	if run := engine.modalDeployments[id]; run != nil {
		delete(engine.modalDeployments, id)
		run.cancel()
	}
}
