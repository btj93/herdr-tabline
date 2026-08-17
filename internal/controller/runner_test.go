package controller_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/controller"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
)

func TestRunnerSubscriptionForcesOneRefreshAndStartsWarmUp(t *testing.T) {
	h := startRunner(t)
	defer h.stop()

	h.waitRefreshes(1, "the forced refresh after subscription")
	if got := h.subscribeCalls(); got != 1 {
		t.Fatalf("subscribe calls = %d, want exactly 1", got)
	}

	// Warm-up is active, so an event must not schedule anything.
	h.sendEvent("tab_moved")
	h.expectNoRefreshBeyond(1)
}

// TestRunnerReplayBurstResetsSettleWithoutRefreshing is the replay guard: Herdr can replay
// arbitrarily stale buffered history with no completion marker, so nothing event-driven may
// run until the stream has been quiet for one poll interval.
func TestRunnerReplayBurstResetsSettleWithoutRefreshing(t *testing.T) {
	h := startRunner(t)
	defer h.stop()
	h.waitRefreshes(1, "the forced refresh after subscription")

	for range 200 {
		h.sendEvent("tab_moved")
	}
	h.expectNoRefreshBeyond(1)
	if resets := h.settle.resets(); resets < 200 {
		t.Fatalf("settle timer resets = %d, want one per replayed event", resets)
	}

	// The poll backstop keeps working throughout warm-up.
	h.tickPoll()
	h.waitRefreshes(2, "the poll refresh during warm-up")
}

func TestRunnerLiveEventRefreshesOnceAfterDebounce(t *testing.T) {
	h := startRunner(t)
	defer h.stop()
	h.waitRefreshes(1, "the forced refresh after subscription")
	h.completeWarmUp()

	h.sendEvent("tab_moved")
	h.expectNoRefreshBeyond(1) // still inside the debounce window
	h.fireDebounce()
	h.waitRefreshes(2, "the debounced refresh")

	// The debounce is disarmed until the next event.
	h.fireDebounce()
	h.expectNoRefreshBeyond(2)
}

func TestRunnerCollapsesABurstIntoOneRefresh(t *testing.T) {
	h := startRunner(t)
	defer h.stop()
	h.waitRefreshes(1, "the forced refresh after subscription")
	h.completeWarmUp()

	for _, kind := range []string{"tab_moved", "tab_created", "pane_focused", "layout_updated", "tab_renamed"} {
		h.sendEvent(kind)
	}
	h.expectNoRefreshBeyond(1)
	h.fireDebounce()
	h.waitRefreshes(2, "one collapsed refresh for the whole burst")
	h.expectNoRefreshBeyond(2)
}

func TestRunnerPollsWithAnEntirelySilentStream(t *testing.T) {
	h := startRunner(t)
	defer h.stop()
	h.waitRefreshes(1, "the forced refresh after subscription")

	for i := 2; i <= 4; i++ {
		h.tickPoll()
		h.waitRefreshes(i, "a poll refresh")
	}
}

func TestRunnerFallsBackToPollingWhenSubscriptionFails(t *testing.T) {
	h := startRunner(t, func(o *harnessOptions) {
		o.subscribeErr = errors.New("events unsupported")
	})
	defer h.stop()

	// No forced refresh, because there was no acknowledgement to force one from.
	h.expectNoRefreshBeyond(0)
	h.tickPoll()
	h.waitRefreshes(1, "the poll refresh with no event stream")
	h.tickPoll()
	h.waitRefreshes(2, "the second poll refresh")

	h.stop()
	if err := h.runErr(); err != nil {
		t.Fatalf("Run returned %v, want nil for a poll-only degradation", err)
	}
}

func TestRunnerResubscribesWithBackoffAfterTheStreamCloses(t *testing.T) {
	h := startRunner(t)
	defer h.stop()
	h.waitRefreshes(1, "the forced refresh after subscription")
	h.completeWarmUp()

	h.closeEvents()
	h.waitSubscribeCalls(2, "the resubscribe")
	h.waitRefreshes(2, "the forced refresh after reconnecting")
	if slept := h.sleeps(); len(slept) != 1 || slept[0] != 250*time.Millisecond {
		t.Fatalf("backoff sleeps = %v, want a single 250ms delay", slept)
	}

	// Warm-up restarted, so an event on the new stream must not schedule a refresh.
	h.sendEvent("tab_moved")
	h.expectNoRefreshBeyond(2)
}

func TestRunnerReturnsPromptlyOnCancellation(t *testing.T) {
	h := startRunner(t)
	h.waitRefreshes(1, "the forced refresh after subscription")

	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return promptly after cancellation")
	}
}

// ---- harness -------------------------------------------------------------------------

type harnessOptions struct {
	subscribeErr error
}

type runnerHarness struct {
	t *testing.T

	mu             sync.Mutex
	refreshes      int
	subscribeCount int
	slept          []time.Duration
	events         chan herdrapi.Event
	subscribeErr   error

	clock    *fakeClock
	settle   *fakeTimer
	debounce *fakeTimer
	ticker   *fakeTicker

	cancel   context.CancelFunc
	done     chan error
	stopOnce sync.Once
}

func startRunner(t *testing.T, apply ...func(*harnessOptions)) *runnerHarness {
	t.Helper()
	options := harnessOptions{}
	for _, fn := range apply {
		fn(&options)
	}

	h := &runnerHarness{
		t:            t,
		events:       make(chan herdrapi.Event, 1024),
		subscribeErr: options.subscribeErr,
		clock:        &fakeClock{},
		done:         make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	runner := controller.NewRunner(controller.RunnerOptions{
		Reconciler: reconcileFunc(func(context.Context, controller.Trigger, string) error {
			h.mu.Lock()
			h.refreshes++
			h.mu.Unlock()
			return nil
		}),
		Client: subscribeOnlyClient{h: h},
		Clock:  h.clock,
		Sleeper: sleeperFunc(func(_ context.Context, d time.Duration) error {
			h.mu.Lock()
			h.slept = append(h.slept, d)
			h.mu.Unlock()
			return nil
		}),
		Logger: testLogger(t),
	})
	go func() { h.done <- runner.Run(ctx) }()

	// The runner creates settle, debounce, then the poll ticker, in that order.
	h.settle = h.clock.waitTimer(t, 0)
	h.debounce = h.clock.waitTimer(t, 1)
	h.ticker = h.clock.waitTicker(t, 0)
	return h
}

func (h *runnerHarness) stop() {
	h.stopOnce.Do(func() {
		h.cancel()
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
			h.t.Error("runner did not stop")
		}
	})
}

func (h *runnerHarness) runErr() error {
	select {
	case err := <-h.done:
		return err
	default:
		return nil
	}
}

func (h *runnerHarness) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refreshes
}

func (h *runnerHarness) subscribeCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.subscribeCount
}

// eventChan reads the current stream under the lock: a reconnect replaces it, so an
// unsynchronized field read here would race the client's Subscribe.
func (h *runnerHarness) eventChan() chan herdrapi.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.events
}

func (h *runnerHarness) sleeps() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Duration(nil), h.slept...)
}

func (h *runnerHarness) waitRefreshes(want int, what string) {
	h.t.Helper()
	waitUntil(h.t, what, func() bool { return h.count() >= want })
	if got := h.count(); got != want {
		h.t.Fatalf("refreshes = %d, want %d (%s)", got, want, what)
	}
}

func (h *runnerHarness) waitSubscribeCalls(want int, what string) {
	h.t.Helper()
	waitUntil(h.t, what, func() bool { return h.subscribeCalls() >= want })
}

// expectNoRefreshBeyond gives the runner a real opportunity to misbehave before asserting.
func (h *runnerHarness) expectNoRefreshBeyond(want int) {
	h.t.Helper()
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := h.count(); got > want {
			h.t.Fatalf("refreshes = %d, want no more than %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *runnerHarness) sendEvent(kind string) {
	h.t.Helper()
	before := h.settle.resets()
	h.eventChan() <- herdrapi.Event{Kind: kind}
	// Wait for the runner to actually consume it, so ordering assertions are meaningful.
	waitUntil(h.t, "the runner to consume an event", func() bool {
		return len(h.eventChan()) == 0 && (h.settle.resets() > before || h.debounce.resets() > 0)
	})
}

func (h *runnerHarness) completeWarmUp() {
	h.t.Helper()
	h.settle.fire()
	// The settle branch has no observable side effect, so drive one event through and
	// confirm it arms the debounce rather than resetting settle again.
	before := h.debounce.resets()
	h.eventChan() <- herdrapi.Event{Kind: "tab_focused"}
	waitUntil(h.t, "warm-up to end", func() bool { return h.debounce.resets() > before })
	h.debounce.drain()
	h.fireDebounce()
	waitUntil(h.t, "the post-warm-up refresh", func() bool { return h.count() >= 2 })
	h.mu.Lock()
	h.refreshes = 1 // normalize so each test's arithmetic starts from the forced refresh
	h.mu.Unlock()
}

func (h *runnerHarness) fireDebounce() { h.debounce.fire() }
func (h *runnerHarness) tickPoll()     { h.ticker.fire() }

func (h *runnerHarness) closeEvents() {
	h.mu.Lock()
	close(h.events)
	h.mu.Unlock()
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ---- fake client, reconciler, sleeper --------------------------------------------------

type reconcileFunc func(context.Context, controller.Trigger, string) error

func (f reconcileFunc) Reconcile(ctx context.Context, trigger controller.Trigger, tabID string) error {
	return f(ctx, trigger, tabID)
}

type sleeperFunc func(context.Context, time.Duration) error

func (f sleeperFunc) Sleep(ctx context.Context, d time.Duration) error { return f(ctx, d) }

type subscribeOnlyClient struct{ h *runnerHarness }

func (c subscribeOnlyClient) Snapshot(context.Context) (herdrapi.Snapshot, error) {
	return herdrapi.Snapshot{}, nil
}

func (c subscribeOnlyClient) ProcessInfo(context.Context, string) (herdrapi.ProcessInfo, error) {
	return herdrapi.ProcessInfo{}, nil
}

func (c subscribeOnlyClient) RenameTab(context.Context, string, string) error { return nil }

func (c subscribeOnlyClient) Subscribe(context.Context, []string) (<-chan herdrapi.Event, error) {
	c.h.mu.Lock()
	c.h.subscribeCount++
	err := c.h.subscribeErr
	if c.h.subscribeCount > 1 {
		// A reconnect gets a fresh stream.
		c.h.events = make(chan herdrapi.Event, 1024)
	}
	events := c.h.events
	c.h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (c subscribeOnlyClient) Close() error { return nil }

// ---- fake clock -------------------------------------------------------------------------

type fakeClock struct {
	mu      sync.Mutex
	timers  []*fakeTimer
	tickers []*fakeTicker
}

func (c *fakeClock) Now() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) }

func (c *fakeClock) NewTimer(time.Duration) controller.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeClock) NewTicker(time.Duration) controller.Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticker := &fakeTicker{ch: make(chan time.Time, 1)}
	c.tickers = append(c.tickers, ticker)
	return ticker
}

func (c *fakeClock) waitTimer(t *testing.T, index int) *fakeTimer {
	t.Helper()
	waitUntil(t, "a timer to be created", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.timers) > index
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timers[index]
}

func (c *fakeClock) waitTicker(t *testing.T, index int) *fakeTicker {
	t.Helper()
	waitUntil(t, "a ticker to be created", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.tickers) > index
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tickers[index]
}

type fakeTimer struct {
	mu         sync.Mutex
	ch         chan time.Time
	resetCount int
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Reset(time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resetCount++
	return true
}

func (t *fakeTimer) Stop() bool { return true }

func (t *fakeTimer) resets() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resetCount
}

func (t *fakeTimer) fire() {
	select {
	case t.ch <- time.Time{}:
	default:
	}
}

func (t *fakeTimer) drain() {
	select {
	case <-t.ch:
	default:
	}
}

type fakeTicker struct{ ch chan time.Time }

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Reset(time.Duration) {}
func (t *fakeTicker) Stop()               {}
func (t *fakeTicker) fire()               { t.ch <- time.Time{} }
