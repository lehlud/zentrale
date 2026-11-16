package agent

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

type inferencePrefix struct {
	id            string
	configuration [32]byte
	messages      [][32]byte
}

func (engine *Engine) completeModel(ctx context.Context, agentID, purpose string, request providers.Request) (providers.Result, error) {
	return engine.traceInference(ctx, agentID, purpose, request, engine.Provider.Complete)
}

func (engine *Engine) traceInference(ctx context.Context, agentID, purpose string, request providers.Request, invoke func(context.Context, providers.Request) (providers.Result, error)) (providers.Result, error) {
	started := time.Now()
	trace := domain.InferenceTrace{ID: NewID(), AgentID: agentID, ProviderID: request.Config.ID, ModelID: request.Model.ID,
		Purpose: purpose, ReasoningLevel: request.ReasoningLevel, CreatedAt: started.UTC(), MessageCount: len(request.Messages), Attempts: []domain.InferenceAttempt{},
		EstimatedSystemTokens: (len(request.System) + 3) / 4, EstimatedToolTokens: (len(JSON(request.Tools)) + 3) / 4}
	account := request.Config.Credential
	if request.Config.OAuth != nil {
		account = request.Config.OAuth.AccountID
		if account == "" {
			account = request.Config.OAuth.AccessToken
		}
	}
	prefix := inferencePrefix{id: trace.ID, configuration: sha256.Sum256([]byte(JSON([]any{
		request.Config.ID, request.Config.BaseURL, request.Config.AuthMode, request.Config.ResponsesTransport, request.Config.PromptCacheMode, account, request.Model.ID,
		request.ReasoningLevel, request.System, request.Tools, request.OutputSchema,
	})))}
	for _, message := range request.Messages {
		prefix.messages = append(prefix.messages, sha256.Sum256([]byte(JSON([]any{message.Role, message.Content, message.Wire, message.CallID, message.ToolName, message.Attachments}))))
		cost := estimatedMessageTokens([]domain.Message{message})
		if message.Role == "context" && strings.HasPrefix(message.Content, "Harness runtime") {
			trace.EstimatedRuntimeTokens += cost
		} else {
			trace.EstimatedConversationTokens += cost
		}
	}
	engine.inferenceMu.Lock()
	if engine.inferencePrefixes == nil {
		engine.inferencePrefixes = map[string]inferencePrefix{}
	}
	key := agentID + ":" + purpose
	previous, found := engine.inferencePrefixes[key]
	trace.PrefixChange = "first_request"
	if found {
		trace.PreviousTraceID = previous.id
		trace.PrefixChange = "configuration_changed"
		if previous.configuration == prefix.configuration {
			for trace.SharedMessages < min(len(previous.messages), len(prefix.messages)) && previous.messages[trace.SharedMessages] == prefix.messages[trace.SharedMessages] {
				trace.SharedMessages++
			}
			trace.PrefixChange = "message_changed"
			if trace.SharedMessages == len(previous.messages) {
				trace.PrefixChange = "append_only"
			} else if len(prefix.messages) < len(previous.messages) && trace.SharedMessages == len(prefix.messages) {
				trace.PrefixChange = "window_shortened"
			}
		}
	}
	if len(engine.inferencePrefixes) >= 256 {
		for old := range engine.inferencePrefixes {
			delete(engine.inferencePrefixes, old)
			break
		}
	}
	engine.inferencePrefixes[key] = prefix
	engine.inferenceMu.Unlock()
	var mutex sync.Mutex
	ctx = providers.WithAttemptObserver(ctx, func(attempt domain.InferenceAttempt) {
		mutex.Lock()
		defer mutex.Unlock()
		attempt.Number = len(trace.Attempts) + 1
		trace.Attempts = append(trace.Attempts, attempt)
	})
	result, err := invoke(ctx, request)
	trace.DurationMillis = int(time.Since(started).Milliseconds())
	trace.Outcome = "completed"
	if err != nil {
		trace.Outcome = "failed"
		if ctx.Err() != nil {
			trace.Outcome = "canceled"
		}
	}
	trace.InputTokens, trace.OutputTokens, trace.CachedTokens = result.InputTokens, result.OutputTokens, result.CachedTokens
	trace.TokensKnown, trace.CacheReadKnown = result.UsageKnown, result.CacheReadKnown
	if saveError := engine.Store.Save("inference_trace", trace.ID, agentID, trace); saveError != nil {
		slog.Warn("save inference trace", "error", saveError)
	}
	result.TraceID, result.Purpose = trace.ID, purpose
	return result, err
}
