package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

const quotaDefaultOutputTokens = 4096

var (
	errMonthlyTokens  = errors.New("account monthly token limit would be exceeded")
	errMonthlyCost    = errors.New("account monthly cost limit would be exceeded")
	errUnknownPrice   = errors.New("model prices are unavailable for an account with a monthly cost limit")
	errOpaqueUsage    = errors.New("this inference endpoint cannot bound usage for the account's monthly limits")
	errUsageUnsettled = errors.New("account inference paused: previous usage could not be recorded")
)

type quotaAmount struct {
	tokens int64
	cost   float64
}

type quotaAccount struct {
	pending      quotaAmount
	blockedUntil time.Time
}

// Pending provider calls count against the same account, even across a UTC
// month boundary, until their usage is persisted or confirmed unavailable.
type usageReservations struct {
	mu       sync.Mutex
	accounts map[string]quotaAccount
}

func (engine *Engine) usageAccount(record domain.Agent) (domain.User, error) {
	owner := record.ChatOwnerID
	if record.ProjectID != "" {
		var project domain.Project
		if err := engine.Store.LoadFields("project", record.ProjectID, &project, []string{"ownerId"}); err != nil {
			return domain.User{}, fmt.Errorf("resolve inference project owner: %w", err)
		}
		owner = project.Owner()
	}

	if owner == "" {
		if record.ID == "" { // Administrative connection test, with no agent.
			return domain.User{}, nil
		}
		return domain.User{}, errors.New("inference agent has no account owner")
	}

	account, err := engine.Store.UserAccount(owner)
	if errors.Is(err, sql.ErrNoRows) && owner == (domain.Project{}).Owner() && record.ProjectID != "" {
		return domain.User{}, nil // Existing installations before account bootstrap.
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("resolve inference account: %w", err)
	}
	if account.Disabled {
		return domain.User{}, errors.New("inference account is disabled")
	}
	return account.User, nil
}

func usesRealInferenceCost(provider domain.Provider) bool {
	return provider.AuthMode != "device" && provider.BillingMode != "fixed"
}

func hasRelevantUsageLimit(account domain.User, provider domain.Provider) bool {
	return account.MonthlyTokenLimit > 0 || (account.MonthlyCostLimit > 0 && usesRealInferenceCost(provider))
}

func (engine *Engine) guardOpaqueUsage(record domain.Agent, provider domain.Provider) error {
	account, err := engine.usageAccount(record)
	if err != nil {
		return err
	}
	if hasRelevantUsageLimit(account, provider) {
		return errOpaqueUsage
	}
	return nil
}

func (engine *Engine) reserveInferenceUsage(ctx context.Context, record domain.Agent, purpose string, request *providers.Request, boundsOutput bool) (func(bool), error) {
	account, err := engine.usageAccount(record)
	if err != nil {
		return nil, err
	}
	if !hasRelevantUsageLimit(account, request.Config) {
		return nil, nil
	}
	if !boundsOutput {
		if purpose == "native_compaction" {
			// This API ignores MaxOutputTokens. Use the quota-checked text fallback.
			return nil, providers.ErrCompactionUnsupported
		}
		return nil, errOpaqueUsage
	}

	reservations := &engine.usageReservations
	reservations.mu.Lock()
	defer reservations.mu.Unlock()

	now := time.Now()
	active := reservations.accounts[account.ID]
	if now.Before(active.blockedUntil) {
		return nil, errUsageUnsettled
	}
	used, err := engine.monthlyInferenceUsage(ctx, account.ID, now)
	if err != nil {
		return nil, fmt.Errorf("read account monthly usage: %w", err)
	}
	used.tokens += active.pending.tokens
	used.cost += active.pending.cost

	next, output, err := inferenceQuotaEstimate(account, *request, used)
	if err != nil {
		return nil, err
	}
	if reservations.accounts == nil {
		reservations.accounts = make(map[string]quotaAccount)
	}
	active.pending.tokens += next.tokens
	active.pending.cost += next.cost
	reservations.accounts[account.ID] = active
	request.MaxOutputTokens = output
	request.SingleAttempt = true

	return func(settled bool) {
		reservations.mu.Lock()
		defer reservations.mu.Unlock()

		active := reservations.accounts[account.ID]
		active.pending.tokens -= next.tokens
		active.pending.cost = max(0, active.pending.cost-next.cost)
		if !settled {
			// Do not admit more potentially billable calls after a write failure.
			active.blockedUntil = quotaMonthStart(time.Now()).AddDate(0, 1, 0)
		}
		if active.pending.tokens == 0 && !time.Now().Before(active.blockedUntil) {
			delete(reservations.accounts, account.ID)
		} else {
			reservations.accounts[account.ID] = active
		}
	}, nil
}

func quotaMonthStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (engine *Engine) monthlyInferenceUsage(ctx context.Context, userID string, now time.Time) (quotaAmount, error) {
	from := quotaMonthStart(now)
	rows, err := engine.Store.UsageReport(ctx, datastore.UsageReportFilter{From: from, To: from.AddDate(0, 1, 0), UserID: userID})
	if err != nil {
		return quotaAmount{}, err
	}

	var total quotaAmount
	for _, row := range rows {
		total.tokens += row.InputTokens + row.OutputTokens
		total.cost += row.RealCost
	}
	return total, nil
}

func inferencePromptEstimate(request providers.Request) int {
	return estimatedPromptTokens(request.System, request.Tools) + (len(JSON(request.OutputSchema))+3)/4
}

func inferenceQuotaEstimate(account domain.User, request providers.Request, used quotaAmount) (quotaAmount, int, error) {
	input := int64(max(1, estimatedMessageTokens(request.Messages)+inferencePromptEstimate(request)))
	output := int64(request.MaxOutputTokens)
	if output <= 0 {
		output = quotaDefaultOutputTokens
	}
	if limit := account.MonthlyTokenLimit; limit > 0 {
		available := limit - used.tokens - input
		if available < 1 {
			return quotaAmount{}, 0, errMonthlyTokens
		}
		output = min(output, available)
	}

	var inputCost, outputRate float64
	if usesRealInferenceCost(request.Config) {
		inputRate, outputPrice, known := quotaPrices(request)
		limit := account.MonthlyCostLimit
		if limit > 0 && !known {
			return quotaAmount{}, 0, errUnknownPrice
		}
		if known {
			inputCost, outputRate = (float64(input)*inputRate)/tokensPerMillion, outputPrice/tokensPerMillion
			if limit > 0 {
				available := limit - used.cost - inputCost
				if available < 0 || (outputRate > 0 && available < outputRate) {
					return quotaAmount{}, 0, errMonthlyCost
				}
				if outputRate > 0 && available < float64(output)*outputRate {
					output = int64(available / outputRate)
				}
			}
		}
	}

	return quotaAmount{tokens: input + output, cost: inputCost + float64(output)*outputRate}, int(output), nil
}

func quotaPrices(request providers.Request) (input, output float64, known bool) {
	model := request.Model
	if !model.PricesKnown {
		return 0, 0, false
	}
	for _, rate := range []float64{model.InputPrice, model.CachedPrice, model.OutputPrice} {
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
			return 0, 0, false
		}
	}
	input = model.InputPrice
	if request.Config.Kind == "openai" {
		input *= 2.5 // Reserve at the highest supported cache-write rate.
	}
	return max(input, model.CachedPrice), model.OutputPrice, true
}
