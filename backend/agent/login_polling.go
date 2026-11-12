package agent

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providerlogin"
)

type loginPoller struct {
	key, id string
	cancel  context.CancelFunc
}

type loginPollState struct {
	status   string
	interval int
}

func loginProtocolDelay(interval int) time.Duration {
	if interval > int((1<<63-1)/time.Second) {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(max(5, interval)) * time.Second
}

func waitLoginProtocol(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// These timers implement the external OAuth device protocol, not UI refresh.
// Clock/wait injection keeps expiry/backoff tests deterministic and offline.
func runLoginProtocol(ctx context.Context, expires time.Time, interval int,
	poll func(context.Context) (loginPollState, error), finish func(string),
	now func() time.Time, wait func(context.Context, time.Duration) error) {
	delay := loginProtocolDelay(interval)
	for {
		if ctx.Err() != nil {
			finish("cancelled")
			return
		}
		remaining := expires.Sub(now())
		if remaining <= 0 {
			finish("expired")
			return
		}
		if poll == nil {
			delay = remaining
		} // Manual/browser flow: expiry only.
		if err := wait(ctx, min(delay, remaining)); err != nil {
			finish("cancelled")
			return
		}
		if ctx.Err() != nil {
			finish("cancelled")
			return
		}
		if !now().Before(expires) {
			finish("expired")
			return
		}
		if poll == nil {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, min(30*time.Second, expires.Sub(now())))
		state, err := poll(attempt)
		cancel()
		if ctx.Err() != nil {
			finish("cancelled")
			return
		}
		if !now().Before(expires) {
			finish("expired")
			return
		}
		if err != nil {
			delay = max(delay, min(max(5*time.Second, min(delay, time.Minute))*2, time.Minute))
			continue
		}
		if state.status != "pending" {
			return
		}
		delay = loginProtocolDelay(state.interval)
	}
}

func (engine *Engine) stopLoginPolling(key, id string) {
	engine.loginPollMu.Lock()
	defer engine.loginPollMu.Unlock()
	for _, poller := range engine.loginPollers {
		if (key == "" || poller.key == key) && (id == "" || poller.id == id) {
			poller.cancel()
		}
	}
}

func (engine *Engine) startLoginPolling(key, id string, expires time.Time, interval int,
	poll func(context.Context) (loginPollState, error), finish func(string)) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if engine.ctx.Err() != nil {
		return engine.ctx.Err()
	}
	engine.loginPollMu.Lock()
	defer engine.loginPollMu.Unlock()
	if engine.loginPollers == nil {
		engine.loginPollers = map[string]*loginPoller{}
	}
	// Cancelled/replaced workers remain counted until they actually exit.
	// Rapid restarts cannot evade the cap by replacing map entries.
	if len(engine.loginPollers) >= 32 {
		return errors.New("too many active login protocols; close a login and retry")
	}
	if engine.loginPollers[id] != nil {
		return errors.New("login protocol already registered")
	}
	for _, previous := range engine.loginPollers {
		if previous.key == key {
			previous.cancel()
		}
	}
	ctx, cancel := context.WithCancel(engine.ctx)
	poller := &loginPoller{key: key, id: id, cancel: cancel}
	engine.loginPollers[id] = poller
	engine.workers.Add(1)
	go func() {
		defer engine.workers.Done()
		defer cancel()
		defer func() { engine.loginPollMu.Lock(); delete(engine.loginPollers, id); engine.loginPollMu.Unlock() }()
		runLoginProtocol(ctx, expires, interval, poll, func(reason string) {
			if engine.ctx.Err() == nil {
				finish(reason)
			}
		}, time.Now, waitLoginProtocol)
	}()
	return nil
}

func (engine *Engine) BeginGitLogin(ctx context.Context, connectionID string) (domain.GitLogin, error) {
	engine.stopLoginPolling("git:"+connectionID, "")
	login, err := engine.GitForge.Begin(ctx, connectionID)
	if err != nil {
		return login, err
	}
	err = engine.startGitLogin(login)
	if err != nil {
		_ = engine.GitForge.EndLogin(connectionID, login.ID, "cancelled")
		return login, err
	}
	engine.Emit("git_login", "", map[string]string{"id": login.ID, "connectionId": connectionID})
	return login, nil
}

func (engine *Engine) startGitLogin(login domain.GitLogin) error {
	connectionID := login.ConnectionID
	return engine.startLoginPolling("git:"+connectionID, login.ID, login.ExpiresAt, login.Interval,
		func(ctx context.Context) (loginPollState, error) {
			value, err := engine.PollGitLogin(ctx, connectionID, login.ID)
			return loginPollState{status: value.Status, interval: value.Interval}, err
		}, func(reason string) {
			if engine.GitForge.EndLogin(connectionID, login.ID, reason) == nil {
				engine.Emit("git_login", "", map[string]string{"id": login.ID, "connectionId": connectionID})
			}
		})
}

// Recovery runs once at startup, never as a side effect of read-only snapshots.
// Persisted protocol state is resumed, not a new authorization grant. Capacity
// errors finalize selected sessions explicitly rather than silently dropping.
func (engine *Engine) recoverLoginProtocols() error {
	for _, connectionID := range []string{"github", "gitlab"} {
		login, err := engine.GitForge.PendingLogin(connectionID)
		if err != nil {
			return err
		}
		if login == nil {
			continue
		}
		if !time.Now().Before(login.ExpiresAt) {
			if err := engine.GitForge.EndLogin(connectionID, login.ID, "expired"); err != nil {
				return err
			}
			continue
		}
		if err := engine.startGitLogin(*login); err != nil {
			return err
		}
	}
	ids, err := engine.Store.ListPendingProviderLogins(32)
	if err != nil {
		return err
	}
	for _, id := range ids {
		var transaction providerlogin.Transaction
		if err := engine.Store.Load("provider_login", id, &transaction); err != nil {
			return err
		}
		if !time.Now().Before(transaction.Session.ExpiresAt) {
			if err := engine.endProviderLogin(id, "expired"); err != nil {
				return err
			}
			continue
		}
		if err := engine.startProviderLogin(transaction.Session); err != nil {
			if err := engine.endProviderLogin(id, "cancelled"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (engine *Engine) PollGitLogin(ctx context.Context, connectionID, loginID string) (domain.GitLogin, error) {
	before, err := engine.GitForge.Login(connectionID, loginID)
	if err != nil {
		return domain.GitLogin{}, err
	}
	if before == nil {
		return domain.GitLogin{Status: "cancelled"}, nil
	}
	value, err := engine.GitForge.Poll(ctx, connectionID, loginID)
	if *before != value {
		engine.Emit("git_login", "", map[string]string{"id": loginID, "connectionId": connectionID})
	}
	if err != nil && ctx.Err() == nil {
		err = errors.New("Git login temporarily unavailable; server will retry until expiry")
	}
	return value, err
}

func (engine *Engine) CancelGitLogin(connectionID, loginID string) error {
	engine.stopLoginPolling("git:"+connectionID, loginID)
	if err := engine.GitForge.EndLogin(connectionID, loginID, "cancelled"); err != nil {
		return err
	}
	engine.Emit("git_login", "", map[string]string{"id": loginID, "connectionId": connectionID})
	return nil
}

func (engine *Engine) StopGitLogin(connectionID string) {
	engine.stopLoginPolling("git:"+connectionID, "")
}
