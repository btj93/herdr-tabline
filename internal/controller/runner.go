package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/btj93/herdr-tabline/internal/herdrapi"
)

// Subscriptions are the structural events that can change a rendered label.
//
// pane.updated is deliberately absent: agent title animation produces roughly ten of them
// per second on the current server, and the poll already covers mutable pane metadata.
var Subscriptions = []string{
	"tab.created", "tab.closed", "tab.renamed", "tab.moved", "tab.focused",
	"workspace.reordered", "workspace.renamed", "workspace.closed", "workspace.focused",
	"pane.created", "pane.closed", "pane.focused", "pane.moved",
	"layout.updated",
}

// backoffSchedule paces reconnection after a transport failure.
var backoffSchedule = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	4 * time.Second,
	5 * time.Second,
}

const (
	fallbackPollInterval    = 2 * time.Second
	fallbackRefreshDebounce = 80 * time.Millisecond
)

// Clock abstracts time so debounce, settle, and poll behavior are testable. Now() alone
// cannot drive those: the tests must decide exactly when each fires.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
	NewTicker(time.Duration) Ticker
}

type Timer interface {
	C() <-chan time.Time
	Reset(time.Duration) bool
	Stop() bool
}

type Ticker interface {
	C() <-chan time.Time
	Reset(time.Duration)
	Stop()
}

// Sleeper paces reconnection attempts and must honor cancellation.
type Sleeper interface {
	Sleep(context.Context, time.Duration) error
}

// Reconciler is the refresh operation the runner drives. *Controller implements it.
type Reconciler interface {
	Reconcile(ctx context.Context, trigger Trigger, targetTabID string) error
}

type RunnerOptions struct {
	Reconciler Reconciler
	Client     herdrapi.Client
	Config     ConfigSource
	Clock      Clock
	Sleeper    Sleeper
	Logger     *slog.Logger
}

// Runner drives reconciliation from the event stream with a poll backstop.
type Runner struct {
	reconciler Reconciler
	client     herdrapi.Client
	config     ConfigSource
	clock      Clock
	sleeper    Sleeper
	log        *slog.Logger
}

func NewRunner(options RunnerOptions) *Runner {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
	}
	clock := options.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	sleeper := options.Sleeper
	if sleeper == nil {
		sleeper = SystemSleeper{}
	}
	return &Runner{
		reconciler: options.Reconciler,
		client:     options.Client,
		config:     options.Config,
		clock:      clock,
		sleeper:    sleeper,
		log:        logger,
	}
}

// Run reconciles until ctx is cancelled. It returns nil on cancellation; a subscription
// that cannot be established is a degradation to poll-only, not a failure.
func (r *Runner) Run(ctx context.Context) error {
	poll, debounce := r.intervals()

	// Creation order is load-bearing for the fake clock in tests: settle, debounce, ticker.
	settle := r.clock.NewTimer(poll)
	defer settle.Stop()
	debounceTimer := r.clock.NewTimer(debounce)
	debounceTimer.Stop()
	defer debounceTimer.Stop()
	ticker := r.clock.NewTicker(poll)
	defer ticker.Stop()

	events, subscribed := r.subscribe(ctx)
	warmingUp := subscribed
	if subscribed {
		// A fresh snapshot immediately after the acknowledgement, because replayed history
		// is never treated as current state.
		r.refresh(ctx, "subscription established")
		settle.Reset(poll)
	}

	dirty := false
	for {
		select {
		case <-ctx.Done():
			return nil

		case event, open := <-events:
			if !open {
				events = nil
				r.log.Warn("event stream closed; reconnecting")
				reconnected, ok := r.reconnect(ctx)
				if ctx.Err() != nil {
					return nil
				}
				if ok {
					events = reconnected
					warmingUp = true
					dirty = false
					debounceTimer.Stop()
					r.refresh(ctx, "stream reconnected")
					poll, debounce = r.applyIntervals(poll, debounce, settle, ticker)
					settle.Reset(poll)
				}
				continue
			}
			if warmingUp {
				// Replay: keep pushing the settle deadline out. No refresh is scheduled,
				// because these payloads may be arbitrarily stale.
				settle.Reset(poll)
				continue
			}
			r.log.Debug("event", "kind", event.Kind)
			dirty = true
			debounceTimer.Reset(debounce)

		case <-settle.C():
			if warmingUp {
				warmingUp = false
				r.log.Debug("replay warm-up complete")
			}

		case <-debounceTimer.C():
			if dirty {
				dirty = false
				r.refresh(ctx, "event")
				poll, debounce = r.applyIntervals(poll, debounce, settle, ticker)
			}

		case <-ticker.C():
			// Unconditional: Herdr has no guaranteed global event for foreground-process
			// changes, so {{ .Process.Name }} cannot be kept current from the stream alone.
			r.refresh(ctx, "poll")
			poll, debounce = r.applyIntervals(poll, debounce, settle, ticker)
		}
	}
}

func (r *Runner) subscribe(ctx context.Context) (<-chan herdrapi.Event, bool) {
	events, err := r.client.Subscribe(ctx, Subscriptions)
	if err != nil {
		// Poll-only is a working degradation, and matches the previous plugin's behavior.
		r.log.Warn("event subscription unavailable; continuing on the poll interval alone", "error", err)
		return nil, false
	}
	return events, true
}

// reconnect retries the subscription on the backoff schedule. It reports the new stream,
// or false if the context ended first.
func (r *Runner) reconnect(ctx context.Context) (<-chan herdrapi.Event, bool) {
	for attempt := 0; ; attempt++ {
		delay := backoffSchedule[min(attempt, len(backoffSchedule)-1)]
		if err := r.sleeper.Sleep(ctx, delay); err != nil {
			return nil, false
		}
		if ctx.Err() != nil {
			return nil, false
		}
		events, err := r.client.Subscribe(ctx, Subscriptions)
		if err == nil {
			r.log.Info("event stream reconnected", "attempts", attempt+1)
			return events, true
		}
		r.log.Warn("resubscribe failed", "attempt", attempt+1, "error", err)
	}
}

func (r *Runner) refresh(ctx context.Context, reason string) {
	if err := r.reconciler.Reconcile(ctx, TriggerAuto, ""); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		r.log.Warn("reconciliation failed", "reason", reason, "error", err)
	}
}

// applyIntervals re-reads the configured durations and retimes the ticker and settle timer
// when a hot reload changed them.
func (r *Runner) applyIntervals(poll, debounce time.Duration, settle Timer, ticker Ticker) (time.Duration, time.Duration) {
	nextPoll, nextDebounce := r.intervals()
	if nextPoll != poll {
		ticker.Reset(nextPoll)
		settle.Reset(nextPoll)
		r.log.Info("poll interval changed", "from", poll, "to", nextPoll)
	}
	if nextDebounce != debounce {
		r.log.Info("refresh debounce changed", "from", debounce, "to", nextDebounce)
	}
	return nextPoll, nextDebounce
}

func (r *Runner) intervals() (time.Duration, time.Duration) {
	if r.config != nil {
		if compiled, ok := r.config.Current(); ok {
			return compiled.PollInterval, compiled.RefreshDebounce
		}
	}
	return fallbackPollInterval, fallbackRefreshDebounce
}

// ---- system implementations ---------------------------------------------------------

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) NewTimer(d time.Duration) Timer { return &systemTimer{timer: time.NewTimer(d)} }

func (SystemClock) NewTicker(d time.Duration) Ticker {
	return &systemTicker{ticker: time.NewTicker(d)}
}

type systemTimer struct{ timer *time.Timer }

func (t *systemTimer) C() <-chan time.Time        { return t.timer.C }
func (t *systemTimer) Reset(d time.Duration) bool { return t.timer.Reset(d) }
func (t *systemTimer) Stop() bool                 { return t.timer.Stop() }

type systemTicker struct{ ticker *time.Ticker }

func (t *systemTicker) C() <-chan time.Time   { return t.ticker.C }
func (t *systemTicker) Reset(d time.Duration) { t.ticker.Reset(d) }
func (t *systemTicker) Stop()                 { t.ticker.Stop() }

type SystemSleeper struct{}

func (SystemSleeper) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
