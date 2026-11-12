package webapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
)

const (
	socketQueryReadLimit   = 128 * 1024
	socketQueryLimit       = 32
	socketQueryTimeout     = 15 * time.Second
	socketQueryResultLimit = 8 * 1024 * 1024
)

type socketQueryRequest struct {
	Type          string         `json:"type"`
	RequestID     string         `json:"requestId"`
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
	OperationName string         `json:"operationName,omitempty"`
}

type socketQueryResponse struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId"`
	Result    json.RawMessage `json:"result"`
}

type socketQuerySubscription struct {
	request    socketQueryRequest
	generation uint64
	dirty      bool
	hasResult  bool
	hash       [sha256.Size]byte
}

type socketQueryWork struct {
	request    socketQueryRequest
	generation uint64
}

type socketQueryResult struct {
	work    socketQueryWork
	encoded json.RawMessage
}

// The socket writer owns this bounded state. No goroutine is created per
// request, subscription or event. Engine events set dirty flags; completion
// schedules at most one coalesced follow-up per changed subscription.
type socketQueries struct {
	subscriptions  map[string]*socketQuerySubscription
	order          []string
	nextGeneration uint64
	busy           bool
}

func newSocketQueries() *socketQueries {
	return &socketQueries{subscriptions: map[string]*socketQuerySubscription{}}
}

func validateSocketQuery(request socketQueryRequest) error {
	if len(request.RequestID) == 0 || len(request.RequestID) > 128 {
		return errors.New("requestId must contain 1..128 bytes")
	}
	if err := ValidateQuery(request.Query, request.OperationName, true); err != nil {
		return err
	}
	document, err := parser.Parse(parser.ParseParams{Source: source.NewSource(&source.Source{Body: []byte(request.Query)})})
	if err != nil {
		return err
	}
	// No mutation, even an unselected named operation, may enter the read-only
	// socket path. Native GraphQL subscriptions are forbidden as well.
	for _, definition := range document.Definitions {
		if operation, ok := definition.(*ast.OperationDefinition); ok && operation.Operation != "query" {
			return errors.New("event snapshots allow query operations only")
		}
	}
	return nil
}

func socketQueryError(id, message string) socketQueryResponse {
	encoded, _ := json.Marshal(map[string]any{"errors": []any{map[string]string{"message": message}}})
	return socketQueryResponse{Type: "query_result", RequestID: id, Result: encoded}
}

func (queries *socketQueries) command(request socketQueryRequest) *socketQueryResponse {
	fail := func(message string) *socketQueryResponse {
		response := socketQueryError(request.RequestID, message)
		return &response
	}
	if len(request.RequestID) == 0 || len(request.RequestID) > 128 {
		return fail("requestId must contain 1..128 bytes")
	}
	if request.Type == "unsubscribe" {
		delete(queries.subscriptions, request.RequestID)
		for index, id := range queries.order {
			if id == request.RequestID {
				queries.order = append(queries.order[:index], queries.order[index+1:]...)
				break
			}
		}
		return nil
	}
	if request.Type != "subscribe" {
		return fail("expected subscribe or unsubscribe")
	}
	if err := validateSocketQuery(request); err != nil {
		return fail(err.Error())
	}
	if queries.subscriptions[request.RequestID] != nil {
		return fail("requestId already subscribed")
	}
	if len(queries.subscriptions) >= socketQueryLimit {
		return fail("maximum 32 snapshot subscriptions")
	}
	queries.nextGeneration++
	queries.subscriptions[request.RequestID] = &socketQuerySubscription{
		request: request, generation: queries.nextGeneration, dirty: true,
	}
	queries.order = append(queries.order, request.RequestID)
	return nil
}

func (queries *socketQueries) invalidate() {
	for _, subscription := range queries.subscriptions {
		subscription.dirty = true
	}
}

func (queries *socketQueries) schedule() *socketQueryWork {
	if queries.busy {
		return nil
	}
	for index, id := range queries.order {
		subscription := queries.subscriptions[id]
		if !subscription.dirty {
			continue
		}
		subscription.dirty = false
		queries.busy = true
		// Round-robin fairness: a hot subscription cannot starve others.
		queries.order = append(queries.order[index+1:], queries.order[:index+1]...)
		return &socketQueryWork{request: subscription.request, generation: subscription.generation}
	}
	return nil
}

func (queries *socketQueries) receive(result socketQueryResult) *socketQueryResponse {
	queries.busy = false
	subscription := queries.subscriptions[result.work.request.RequestID]
	if subscription == nil || subscription.generation != result.work.generation {
		return nil
	}
	hash := sha256.Sum256(result.encoded)
	if subscription.hasResult && subscription.hash == hash {
		return nil
	}
	subscription.hash, subscription.hasResult = hash, true
	return &socketQueryResponse{Type: "query_result", RequestID: result.work.request.RequestID, Result: result.encoded}
}

type socketQueryTask struct {
	ctx     context.Context
	work    socketQueryWork
	execute func(context.Context, socketQueryRequest) any
	results chan<- socketQueryResult
}

// Explicit ownership keeps each deadline/cancel pair together across select
// iterations. Socket exit and normal result consumption both release it.
type socketQueryExecution struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func startSocketQueryExecution(parent context.Context) *socketQueryExecution {
	ctx, cancel := context.WithTimeout(parent, socketQueryTimeout)
	return &socketQueryExecution{ctx: ctx, cancel: cancel}
}

func (execution *socketQueryExecution) done() <-chan struct{} {
	if execution == nil {
		return nil
	}
	return execution.ctx.Done()
}

func (execution *socketQueryExecution) stop() {
	if execution != nil {
		execution.cancel()
	}
}

type socketQueryPool struct {
	queue   chan socketQueryTask
	stopped []<-chan struct{}
}

var socketQueryPoolOnce sync.Once
var sharedSocketQueryPool *socketQueryPool

// One process-wide pool also bounds executions across disconnect/reconnect and
// multiple handlers. A resolver ignoring cancellation can occupy a slot until
// it returns, but cannot cause unlimited replacement workers or executions.
func eventSocketQueryPool() *socketQueryPool {
	socketQueryPoolOnce.Do(func() { sharedSocketQueryPool = newSocketQueryPool(context.Background(), 4, 16) })
	return sharedSocketQueryPool
}

func newSocketQueryPool(ctx context.Context, workers, queueSize int) *socketQueryPool {
	pool := &socketQueryPool{queue: make(chan socketQueryTask, queueSize)}
	for range workers {
		stopped := make(chan struct{})
		pool.stopped = append(pool.stopped, stopped)
		go func() {
			defer close(stopped)
			for {
				select {
				case <-ctx.Done():
					return
				case task := <-pool.queue:
					if ctx.Err() != nil {
						return
					}
					if task.ctx.Err() != nil {
						continue
					}
					// graphql-go Execute starts an inner execution goroutine and
					// otherwise returns early on ctx.Done. Mask cancellation for
					// the executor so this fixed slot tracks actual resolver end.
					value := task.execute(context.WithoutCancel(task.ctx), task.work.request)
					if task.ctx.Err() != nil {
						continue
					}
					encoded, err := json.Marshal(value)
					if err != nil {
						encoded = socketQueryError(task.work.request.RequestID, "cannot encode snapshot").Result
					}
					if len(encoded) > socketQueryResultLimit {
						encoded = socketQueryError(task.work.request.RequestID, "snapshot exceeds 8 MiB; narrow the query").Result
					}
					select {
					case task.results <- socketQueryResult{work: task.work, encoded: encoded}:
					case <-task.ctx.Done():
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	return pool
}

func (pool *socketQueryPool) submit(task socketQueryTask) bool {
	if task.ctx.Err() != nil {
		return false
	}
	select {
	case pool.queue <- task:
		return true
	default:
		return false
	}
}

func (server *Server) executeSocketQuery(ctx context.Context, schema graphql.Schema, token, client string, request socketQueryRequest) any {
	if !server.Auth.Valid(token) {
		return socketQueryError(request.RequestID, "authentication required").Result
	}
	ctx = context.WithValue(ctx, requestKey{}, &requestState{authenticated: true, client: client})
	return graphql.Do(graphql.Params{Schema: schema, RequestString: request.Query,
		VariableValues: request.Variables, OperationName: request.OperationName, Context: ctx})
}
