package agent

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/hosted"
)

func TestSetupModalHumanizerDeploysAndSelectsModel(t *testing.T) {
	engine, _ := testEngine(t, nil)
	calls := make(chan string, 2)
	finish := make(chan struct{})
	engine.deployModal = func(ctx context.Context, credential, app, model string) (string, error) {
		calls <- credential + " " + app + " " + model
		select {
		case <-finish:
			return "modal://test/" + app + "/run_batch", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	pending, err := engine.SetupModalHumanizer("ak-fixture", "as-fixture", "")
	if err != nil || pending.CatalogStatus != modalDeploying || pending.Connected || pending.Credential != "" {
		t.Fatalf("expected a public, pending provider, got %#v, %v", pending, err)
	}
	app := engine.modalAppName(pending.ID)
	select {
	case call := <-calls:
		if want := "ak-fixture.as-fixture " + app + " " + hosted.ModalHumanizer; call != want {
			t.Fatalf("unexpected deployment arguments: %s", call)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("deployment did not start")
	}
	if _, err := engine.SetupModalHumanizer("", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SetupModalHumanizer("ak-other", "as-other", ""); err == nil {
		t.Fatal("deployment accepted a different token while in progress")
	}
	var public domain.Provider
	if err := engine.Store.Load("provider", pending.ID, &public); err != nil || public.Credential != "" {
		t.Fatal("provider row should omit the credential", err)
	}
	credential, err := engine.Store.Credential("modal_provider/" + pending.ID)
	if err != nil || string(credential) != "ak-fixture.as-fixture" {
		t.Fatal("encrypted credential was not saved", err)
	}
	if _, err := engine.hostedProvider(pending.ID); err == nil {
		t.Fatal("the deployment is still pending")
	}

	close(finish)
	expected := domain.HostedHumanizer{ProviderID: pending.ID, ModelID: hosted.ModalHumanizer}
	deadline := time.Now().Add(3 * time.Second)
	for {
		settings, err := engine.HumanizerSettings()
		if err != nil {
			t.Fatal(err)
		}
		if settings.Hosted == expected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Humanizer was not selected after deployment: %#v", settings)
		}
		time.Sleep(10 * time.Millisecond)
	}
	provider, err := engine.hostedProvider(pending.ID)
	if err != nil || provider.BaseURL != "modal://test/"+app+"/run_batch" {
		t.Fatalf("invalid deployed provider: %s, %v", provider.BaseURL, err)
	}
	select {
	case <-calls:
		t.Fatal("identical setup was deployed twice")
	default:
	}
	expected.ModelID = hosted.ModalSmaug
	if err := engine.SaveHumanizer(domain.HumanizerSettings{Hosted: expected}); err == nil {
		t.Fatal("accepted a different model from the one deployed")
	}
}

func TestRecoveredModalSetupCanBeDisconnected(t *testing.T) {
	engine, _ := testEngine(t, nil)
	const id = "modal-humanizer-smaug"
	provider := domain.Provider{
		ID: id, Kind: "modal", AuthMode: "api", Credential: "ak-fixture.as-fixture",
		ModalModelID: hosted.ModalSmaug, CatalogStatus: modalDeploying,
	}
	if err := engine.saveProvider(provider); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	canceled := make(chan struct{})
	engine.deployModal = func(ctx context.Context, credential, app, model string) (string, error) {
		if credential != provider.Credential || app != engine.modalAppName(id) || model != provider.ModalModelID {
			t.Error("recovery did not use the saved deployment")
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		return "modal://test/" + app + "/run_batch", nil
	}
	if err := engine.recoverModalDeployments(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("saved deployment did not resume")
	}
	if err := engine.DisconnectProvider(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not cancel the deployment")
	}
	engine.Close()
	var saved domain.Provider
	if err := engine.Store.Load("provider", id, &saved); err != nil || saved.CatalogStatus != "disconnected" || saved.Connected {
		t.Fatalf("provider should stay disconnected, got %#v, %v", saved, err)
	}
	if _, err := engine.Store.Credential("modal_provider/" + id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("encrypted token should be removed", err)
	}
}
