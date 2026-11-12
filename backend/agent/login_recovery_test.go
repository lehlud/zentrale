package agent

import (
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providerlogin"
)

func TestEngineStartupResumesPendingLoginAndExpiresStaleGrant(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Browser login needs only a local expiry timer: this fixture can never
	// contact a real provider, even if CI pauses for longer than an interval.
	active := providerlogin.Transaction{Session: domain.ProviderLogin{ID: "active", ProviderID: "manual-provider", Method: "browser", Status: "pending", ExpiresAt: time.Now().Add(time.Hour)}, CodeVerifier: "private-verifier"}
	stale := providerlogin.Transaction{Session: domain.ProviderLogin{ID: "stale", ProviderID: "expired-provider", Method: "device", Status: "pending", ExpiresAt: time.Now().Add(-time.Hour)}, DeviceAuthorizationID: "private-device"}
	for _, value := range []providerlogin.Transaction{active, stale} {
		if err := store.Save("provider_login", value.Session.ID, value.Session.ProviderID, value); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		engine, err := New(store, nil)
		if err != nil {
			t.Fatal(err)
		}
		engine.loginPollMu.Lock()
		count := len(engine.loginPollers)
		registered := engine.loginPollers["active"] != nil
		engine.loginPollMu.Unlock()
		if count != 1 || !registered {
			engine.Close()
			t.Fatal("pending session not resumed", count, registered)
		}
		var expired providerlogin.Transaction
		if err := store.Load("provider_login", "stale", &expired); err != nil || expired.Session.Status != "expired" || expired.DeviceAuthorizationID != "" {
			engine.Close()
			t.Fatal(expired.Session, err)
		}
		engine.Close()
		var saved providerlogin.Transaction
		if err := store.Load("provider_login", "active", &saved); err != nil || saved.Session.Status != "pending" || saved.CodeVerifier != "private-verifier" {
			t.Fatal("shutdown discarded resumable browser verifier", saved.Session, err)
		}
	}
}
