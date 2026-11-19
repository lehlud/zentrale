package hosted

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/lehlud/zentrale/domain"
)

const predictionResource = "replicate_prediction"

type pendingPrediction struct {
	ID         string    `json:"id"`
	ProviderID string    `json:"providerId"`
	Owner      string    `json:"owner"`
	Expires    time.Time `json:"expires"`
}

// RecoverPredictions cancels accepted Replicate jobs left by a previous
// Zentrale process. The provider's own deadline covers a lost receipt or an
// installation that never comes back online.
func (client *Client) RecoverPredictions(ctx context.Context) {
	if client.store == nil {
		return
	}
	var pending []pendingPrediction
	const page = 500
	for offset := 0; ctx.Err() == nil; offset += page {
		rows, err := client.store.ListFields(predictionResource, nil, page, offset, []string{"id"})
		if err != nil {
			slog.Warn("could not read interrupted Replicate predictions", "error", err)
			return
		}
		for _, row := range rows {
			id, _ := row["id"].(string)
			var receipt pendingPrediction
			if client.store.Load(predictionResource, id, &receipt) == nil && receipt.Owner != client.owner && slug.MatchString(id) && receipt.ID == id {
				pending = append(pending, receipt)
			}
		}
		if len(rows) < page {
			break
		}
	}
	for _, receipt := range pending {
		if ctx.Err() != nil {
			return
		}
		if err := client.recoverPrediction(ctx, receipt); err != nil {
			slog.Warn("could not cancel interrupted Replicate prediction", "error", err)
		}
	}
}

func (client *Client) recoverPrediction(ctx context.Context, receipt pendingPrediction) error {
	// Allow a little time for submission and server-side timer rounding.
	if time.Now().Before(receipt.Expires.Add(15 * time.Second)) {
		var provider domain.Provider
		if err := client.store.Load("provider", receipt.ProviderID, &provider); err != nil {
			return err
		}
		if provider.Kind != "replicate" || provider.Credential == "" || ValidateProvider(&provider) != nil {
			return errors.New("the original Replicate account is unavailable")
		}
		for attempt := 0; ; attempt++ {
			err := client.cancelPrediction(ctx, provider, receipt.ID)
			var status statusError
			if err == nil || (errors.As(err, &status) && status.status == http.StatusNotFound) {
				break
			}
			if attempt == 2 || ctx.Err() != nil || (errors.As(err, &status) && status.status < 500) {
				return err
			}
			if err := client.wait(ctx); err != nil {
				return err
			}
		}
	}
	return client.store.Remove(predictionResource, receipt.ID)
}
