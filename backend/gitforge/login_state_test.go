package gitforge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPublicLoginStateCancellationExpiryAndReplacementIsolation(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newForgeFixture(t, kind)
			login, err := fixture.service.Begin(context.Background(), kind)
			if err != nil {
				t.Fatal(err)
			}
			public, err := fixture.service.Login(kind, login.ID)
			if err != nil || public == nil || public.Status != "pending" {
				t.Fatal(public, err)
			}
			body, _ := json.Marshal(public)
			for _, secret := range []string{"private-device", "access-one", "secret", "deviceCode", "accessToken"} {
				if strings.Contains(string(body), secret) {
					t.Fatal("public login leaked credentials", string(body))
				}
			}
			if err := fixture.service.EndLogin(kind, login.ID, "cancelled"); err != nil {
				t.Fatal(err)
			}
			value, err := fixture.service.Poll(context.Background(), kind, login.ID)
			if err != nil || value.Status != "cancelled" || fixture.tokenPosts != 0 {
				t.Fatal(value, err, fixture.tokenPosts)
			}
			next, err := fixture.service.Begin(context.Background(), kind)
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.service.EndLogin(kind, login.ID, "expired"); err != nil {
				t.Fatal(err)
			}
			if public, err := fixture.service.Login(kind, next.ID); err != nil || public == nil || public.Status != "pending" {
				t.Fatal(public, err)
			}
			if old, err := fixture.service.Login(kind, login.ID); err != nil || old != nil {
				t.Fatal(old, err)
			}
			fixture.now = next.ExpiresAt.Add(time.Second)
			value, err = fixture.service.Poll(context.Background(), kind, next.ID)
			if err != nil || value.Status != "expired" || fixture.tokenPosts != 0 {
				t.Fatal(value, err)
			}
			stored, err := fixture.service.account(kind)
			if err != nil || stored.Login.DeviceCode != "" {
				t.Fatal("expired device secret retained", err)
			}
		})
	}
}
