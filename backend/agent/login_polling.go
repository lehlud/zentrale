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

func gitPollKey(owner, connection string) string { return "git:" + owner + ":" + connection }

func (engine *Engine) BeginGitLogin(ctx context.Context, owner, connectionID string) (domain.GitLogin, error) {
	engine.StopGitLogin(owner, connectionID)
	service := engine.GitFor(owner)
	login, err := service.Begin(ctx, connectionID)
	if err != nil {
		return login, err
	}
	err = engine.startGitLogin(owner, login)
	if err != nil {
		_ = service.EndLogin(connectionID, login.ID, "cancelled")
		return login, err
	}
	engine.Emit("resources", "", nil)
	return login, nil
}

func (engine *Engine) startGitLogin(owner string, login domain.GitLogin) error {
	connectionID := login.ConnectionID
	// Browser flows only inspect session/expiry while awaiting consent. Once the
	// code has been exchanged, the same worker can retry a transient profile read
	// without ever replaying that code or starting a device authorization request.
	poll := func(ctx context.Context) (loginPollState, error) {
		value, err := engine.PollGitLogin(ctx, owner, connectionID, login.ID)
		return loginPollState{status: value.Status, interval: value.Interval}, err
	}

	return engine.startLoginPolling(gitPollKey(owner, connectionID), login.ID, login.ExpiresAt, login.Interval,
		poll, func(reason string) {
			if engine.GitFor(owner).EndLogin(connectionID, login.ID, reason) == nil {
				engine.Emit("resources", "", nil)
			}
		})
}

// Recovery runs once at startup, never as a side effect of read-only snapshots.
// Persisted protocol state is resumed, not a new authorization grant. Capacity
// errors finalize selected sessions explicitly rather than silently dropping.
func (engine *Engine) recoverLoginProtocols() error {
	users, err := engine.Store.Users()
	if err != nil {
		return err
	}
	for _, user := range users {
		if !user.Disabled {
			if err := engine.recoverGitLogins(user.ID); err != nil {
				return err
			}
		}
	}
	if len(users) == 0 {
		if err := engine.recoverGitLogins(domain.InitialUserID); err != nil {
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

func (engine *Engine) recoverGitLogins(owner string) error {
	service := engine.GitFor(owner)
	connections, err := service.Connections()
	if err != nil {
		return err
	}

	for _, connection := range connections {
		login, err := service.PendingLogin(connection.ID)
		if err != nil {
			return err
		}
		if login == nil {
			continue
		}
		if !time.Now().Before(login.ExpiresAt) {
			err = service.EndLogin(connection.ID, login.ID, "expired")
		} else {
			err = engine.startGitLogin(owner, *login)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (engine *Engine) PollGitLogin(ctx context.Context, owner, connectionID, loginID string) (domain.GitLogin, error) {
	service := engine.GitFor(owner)
	before, err := service.Login(connectionID, loginID)
	if err != nil {
		return domain.GitLogin{}, err
	}
	if before == nil {
		return domain.GitLogin{Status: "cancelled"}, nil
	}
	value, err := service.Poll(ctx, connectionID, loginID)
	if *before != value {
		engine.Emit("resources", "", nil)
	}
	if err != nil && ctx.Err() == nil {
		err = errors.New("Git login temporarily unavailable; server will retry until expiry")
	}
	return value, err
}

func (engine *Engine) CancelGitLogin(owner, connectionID, loginID string) error {
	engine.stopLoginPolling(gitPollKey(owner, connectionID), loginID)
	if err := engine.GitFor(owner).EndLogin(connectionID, loginID, "cancelled"); err != nil {
		return err
	}
	engine.Emit("resources", "", nil)
	return nil
}

func (engine *Engine) StopGitLogin(owner, connectionID string) {
	engine.stopLoginPolling(gitPollKey(owner, connectionID), "")
}
