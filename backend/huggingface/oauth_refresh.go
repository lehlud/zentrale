package huggingface

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func (service *Service) refreshOAuth(ctx context.Context) error {
	service.refreshMu.Lock()
	defer service.refreshMu.Unlock()

	service.mu.Lock()
	value, err := service.account()
	if err != nil || value.OAuth == nil {
		service.mu.Unlock()
		return err
	}

	remaining := time.Until(value.OAuth.ExpiresAt)
	if remaining >= time.Minute || (value.OAuth.RefreshToken == "" && remaining > 0) {
		service.mu.Unlock()
		return nil
	}

	if service.closed {
		service.mu.Unlock()
		return errors.New("Hugging Face connection service is closed")
	}

	epoch := service.epoch
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	ctx, release := service.requestContext(ctx)
	service.mu.Unlock()
	defer cancel()
	defer release()

	var tokens oauthTokens
	if value.OAuth.RefreshToken == "" {
		err = errReauthorize
	} else {
		tokens, err = service.oauthTokens(ctx, value.App, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {value.OAuth.RefreshToken},
		})
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	if service.epoch != epoch || service.closed || ctx.Err() != nil {
		return errors.New("Hugging Face connection changed during refresh; retry")
	}

	current, loadErr := service.account()
	if loadErr != nil {
		return loadErr
	}

	if errors.Is(err, errReauthorize) {
		current.Public = domain.HuggingFaceConnection{}
		current.Token = ""
		current.OAuth = nil
		if saveErr := service.saveAccount(current); saveErr != nil {
			return saveErr
		}

		service.invalidateRequests()
		return err
	}

	if err != nil {
		return err
	}

	current.Token = tokens.AccessToken
	current.OAuth.ExpiresAt = tokens.ExpiresAt
	if tokens.RefreshToken != "" {
		current.OAuth.RefreshToken = tokens.RefreshToken
	}

	return service.saveAccount(current)
}
