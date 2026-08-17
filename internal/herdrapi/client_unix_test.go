package herdrapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/herdrapi"
)

// testTimeout bounds every synchronization point in this package so a
// dial/write/read ordering regression fails with a diagnostic instead of
// hanging until the external test runner kills the package.
const testTimeout = 3 * time.Second

// testMaxFrameBytes mirrors the transport's unexported maxFrameBytes limit.
const testMaxFrameBytes = 1 << 20

// teardownWindow bounds an assertion that a specific teardown call — cancel() or Close() —
// closed the event channel. It is deliberately far tighter than testTimeout so that a pass
// is attributable to the call under test rather than to a watchdog or a socket deadline
// expiring at roughly the same moment.
const teardownWindow = 500 * time.Millisecond

type protocolRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func TestUnixClientOrdinaryCallsUseFreshConnectionsAndRecordedFixtures(t *testing.T) {
	listener, socket := newUnixListener(t)
	requests := make(chan protocolRequest, 3)
	serverErrors := make(chan error, 3)
	fixtures := map[string]json.RawMessage{
		"session.snapshot": readFixture(t, "snapshot.json"),
		"pane-codex":       readFixture(t, "process_info_codex.json"),
		"pane-claude":      readFixture(t, "process_info_claude.json"),
	}
	go func() {
		for range 3 {
			conn, err := listener.Accept()
			if err != nil {
				serverErrors <- err
				continue
			}
			go func(conn net.Conn) {
				defer conn.Close()
				req, err := readProtocolRequest(conn)
				if err != nil {
					serverErrors <- err
					return
				}
				requests <- req
				result := fixtures[req.Method]
				if req.Method == "pane.process_info" {
					var params struct {
						PaneID string `json:"pane_id"`
					}
					if err := json.Unmarshal(req.Params, &params); err != nil {
						serverErrors <- err
						return
					}
					result = fixtures[params.PaneID]
				}
				if len(result) == 0 {
					serverErrors <- fmt.Errorf("unexpected request: %s %s", req.Method, req.Params)
					return
				}
				serverErrors <- writeResponse(conn, req.ID, result)
			}(conn)
		}
	}()

	client := herdrapi.NewUnixClient(socket)
	t.Cleanup(func() { closeWithin(t, client) })
	ctx := testContext(t)

	var snapshot herdrapi.Snapshot
	infos := make([]herdrapi.ProcessInfo, 2)
	var callErrors [3]error
	var calls sync.WaitGroup
	calls.Add(3)
	go func() { defer calls.Done(); snapshot, callErrors[0] = client.Snapshot(ctx) }()
	go func() { defer calls.Done(); infos[0], callErrors[1] = client.ProcessInfo(ctx, "pane-codex") }()
	go func() { defer calls.Done(); infos[1], callErrors[2] = client.ProcessInfo(ctx, "pane-claude") }()
	awaitGroup(t, &calls, "concurrent ordinary calls")
	for _, err := range callErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(snapshot.Tabs) == 0 || snapshot.Tabs[0].TabID == "" {
		t.Fatalf("snapshot fixture was not decoded: %#v", snapshot)
	}
	if infos[0].PaneID != "w5:p6" || infos[1].PaneID != "w5:pB" {
		t.Fatalf("process responses crossed connections: %#v", infos)
	}

	seen := make([]string, 0, 3)
	ids := make(map[string]bool, 3)
	for range 3 {
		req := awaitRequest(t, requests, "recorded request")
		if req.ID == "" || ids[req.ID] {
			t.Fatalf("request ID is empty or reused: %q", req.ID)
		}
		ids[req.ID] = true
		seen = append(seen, req.Method+":"+string(req.Params))
		if err := awaitError(t, serverErrors, "server response"); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(seen)
	want := []string{
		`pane.process_info:{"pane_id":"pane-claude"}`,
		`pane.process_info:{"pane_id":"pane-codex"}`,
		`session.snapshot:{}`,
	}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("requests = %v, want %v", seen, want)
	}
}

func TestUnixClientLimitsOrdinaryConnectionsAndHonorsContext(t *testing.T) {
	listener, socket := newUnixListener(t)
	release := make(chan struct{})
	accepted := make(chan struct{}, 9)
	serverErrors := make(chan error, 9)
	var active atomic.Int32
	var maximum atomic.Int32
	go func() {
		for range 9 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				req, err := readProtocolRequest(conn)
				if err != nil {
					serverErrors <- err
					return
				}
				current := active.Add(1)
				defer active.Add(-1)
				for {
					prior := maximum.Load()
					if current <= prior || maximum.CompareAndSwap(prior, current) {
						break
					}
				}
				accepted <- struct{}{}
				<-release
				serverErrors <- writeResponse(conn, req.ID, json.RawMessage(`{}`))
			}(conn)
		}
	}()

	client := herdrapi.NewUnixClient(socket)
	t.Cleanup(func() { closeWithin(t, client) })
	ctx := testContext(t)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, err := client.ProcessInfo(ctx, "pane"); errs <- err }()
	}
	for i := 0; i < 8; i++ {
		select {
		case <-accepted:
		case <-ctx.Done():
			t.Fatal("eight ordinary calls were not admitted")
		}
	}

	blockedCtx, blockedCancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer blockedCancel()
	if _, err := client.ProcessInfo(blockedCtx, "ninth"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ninth call error = %v, want context deadline", err)
	}
	select {
	case <-accepted:
		t.Fatal("ninth ordinary connection was accepted while eight were active")
	default:
	}
	close(release)
	for range 8 {
		if err := awaitError(t, errs, "released ordinary call"); err != nil {
			t.Fatal(err)
		}
		if err := awaitError(t, serverErrors, "released server response"); err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() > 8 {
		t.Fatalf("maximum ordinary connections = %d, want at most 8", maximum.Load())
	}
}

func TestUnixClientRejectsInvalidOrdinaryResponses(t *testing.T) {
	tests := []struct {
		name     string
		response func(protocolRequest) string
		contains string
	}{
		{name: "mismatched ID", response: func(protocolRequest) string { return `{"id":"wrong","result":{}}` + "\n" }, contains: "response id"},
		{name: "RPC error", response: func(req protocolRequest) string {
			return fmt.Sprintf(`{"id":%q,"error":{"code":"agent_not_found","message":"denied"}}`, req.ID) + "\n"
		}, contains: "rpc error agent_not_found: denied"},
		{name: "malformed JSON", response: func(protocolRequest) string { return "not-json\n" }, contains: "decode response"},
		{name: "extra response line", response: func(req protocolRequest) string { return fmt.Sprintf(`{"id":%q,"result":{}}`, req.ID) + "\n{}\n" }, contains: "extra response"},
		{name: "missing newline", response: func(req protocolRequest) string { return fmt.Sprintf(`{"id":%q,"result":{}}`, req.ID) }, contains: "newline"},
		{name: "no outcome member", response: func(req protocolRequest) string {
			return fmt.Sprintf(`{"id":%q}`, req.ID) + "\n"
		}, contains: "neither result nor error"},
		{name: "both outcome members", response: func(req protocolRequest) string {
			return fmt.Sprintf(`{"id":%q,"result":{},"error":{"code":"denied","message":"denied"}}`, req.ID) + "\n"
		}, contains: "both result and error"},
		{name: "oversized frame", response: func(protocolRequest) string {
			return string(bytes.Repeat([]byte("x"), testMaxFrameBytes+1024))
		}, contains: "frame exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener, socket := newUnixListener(t)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				req, err := readProtocolRequest(conn)
				if err == nil {
					_, _ = conn.Write([]byte(test.response(req)))
				}
			}()
			client := herdrapi.NewUnixClient(socket)
			t.Cleanup(func() { closeWithin(t, client) })
			_, err := client.ProcessInfo(testContext(t), "pane")
			if err == nil || !strings.Contains(err.Error(), test.contains) || !strings.Contains(err.Error(), "pane.process_info") || !strings.Contains(err.Error(), socket) {
				t.Fatalf("error = %v, want method, socket, and %q", err, test.contains)
			}
		})
	}
}

func TestUnixClientRenameSendsOnlyTabIDAndLabel(t *testing.T) {
	listener, socket := newUnixListener(t)
	request := make(chan protocolRequest, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := readProtocolRequest(conn)
		if err != nil {
			return
		}
		request <- req
		_ = writeResponse(conn, req.ID, json.RawMessage(`{"type":"ok"}`))
	}()
	client := herdrapi.NewUnixClient(socket)
	t.Cleanup(func() { closeWithin(t, client) })
	if err := client.RenameTab(testContext(t), "tab-7", "api tests"); err != nil {
		t.Fatal(err)
	}
	req := awaitRequest(t, request, "rename request")
	if req.Method != "tab.rename" || string(req.Params) != `{"tab_id":"tab-7","label":"api tests"}` {
		t.Fatalf("rename request = %s %s", req.Method, req.Params)
	}
}

// TestUnixClientRenameRejectsResponseWithoutOutcome guards the result-less
// method path: RenameTab passes a nil destination, so envelope validation is
// the only thing standing between a malformed response and a false success.
func TestUnixClientRenameRejectsResponseWithoutOutcome(t *testing.T) {
	listener, socket := newUnixListener(t)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := readProtocolRequest(conn)
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte(fmt.Sprintf(`{"id":%q}`, req.ID) + "\n"))
	}()
	client := herdrapi.NewUnixClient(socket)
	t.Cleanup(func() { closeWithin(t, client) })
	err := client.RenameTab(testContext(t), "tab-7", "api tests")
	if err == nil {
		t.Fatal("rename reported success for a response carrying no outcome")
	}
	if !strings.Contains(err.Error(), "neither result nor error") || !strings.Contains(err.Error(), "tab.rename") {
		t.Fatalf("error = %v, want tab.rename and the missing-outcome reason", err)
	}
}

func TestUnixClientCloseCancelsStalledOrdinaryCall(t *testing.T) {
	listener, socket := newUnixListener(t)
	requestRead := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := readProtocolRequest(conn); err == nil {
			close(requestRead)
		}
		_, _ = bufio.NewReader(conn).ReadByte()
	}()
	client := herdrapi.NewUnixClient(socket)
	callDone := make(chan error, 1)
	go func() { _, err := client.Snapshot(context.Background()); callDone <- err }()
	awaitSignal(t, requestRead, "server to read the stalled request")
	closed := make(chan struct{})
	go func() { _ = client.Close(); close(closed) }()
	awaitSignal(t, closed, "Close to return past a stalled ordinary call")
	if err := awaitError(t, callDone, "stalled ordinary call to return"); err == nil {
		t.Fatal("stalled call succeeded after Close")
	}
	if _, err := client.Snapshot(context.Background()); err == nil {
		t.Fatal("call after Close succeeded")
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

// newStreamContext returns a cancel-only context for a subscription that must outlive its
// handshake. A watchdog cancels it after testTimeout so a server that accepts but never
// acknowledges still fails with a diagnostic instead of hanging the package. The context
// deliberately carries no deadline: Subscribe applies a context deadline to the stream
// socket and never clears it, so a deadline here would close the event channel on its own
// and let the cancel() and Close() assertions pass for the wrong reason.
func newStreamContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	watchdog := time.AfterFunc(testTimeout, cancel)
	t.Cleanup(func() {
		watchdog.Stop()
		cancel()
	})
	return ctx, cancel
}

// closeWithin bounds client.Close, which ends in an unbounded WaitGroup wait. Calling it
// bare on the test goroutine — including from t.Cleanup, which also runs there — lets a
// lifecycle-accounting regression hang the whole package with no diagnostic.
func closeWithin(t *testing.T, client herdrapi.Client) {
	t.Helper()
	closed := make(chan struct{})
	go func() { _ = client.Close(); close(closed) }()
	awaitSignal(t, closed, "client Close to return")
}

func awaitSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func awaitError(t *testing.T, errs <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-errs:
		return err
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

func awaitRequest(t *testing.T, requests <-chan protocolRequest, what string) protocolRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
		return protocolRequest{}
	}
}

func awaitGroup(t *testing.T, group *sync.WaitGroup, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() { group.Wait(); close(done) }()
	awaitSignal(t, done, what)
}

func acceptWithin(t *testing.T, listener net.Listener, what string) net.Conn {
	t.Helper()
	type accepted struct {
		conn net.Conn
		err  error
	}
	results := make(chan accepted, 1)
	go func() {
		conn, err := listener.Accept()
		results <- accepted{conn: conn, err: err}
	}()
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("accept %s: %v", what, result.err)
		}
		// Bound the accepted connection too. Bounding only the Accept would leave the
		// read that immediately follows it able to stall the package indefinitely.
		if err := result.conn.SetDeadline(time.Now().Add(testTimeout)); err != nil {
			t.Fatalf("set deadline on %s: %v", what, err)
		}
		t.Cleanup(func() { _ = result.conn.Close() })
		return result.conn
	case <-time.After(testTimeout):
		t.Fatalf("timed out accepting %s", what)
		return nil
	}
}

func newUnixListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	socket := newSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener, socket
}

// newSocketPath returns a unique path inside a short private directory. A short
// root matters because Unix socket addresses are length-limited, and a unique
// directory keeps a "missing" socket path from colliding with another process.
func newSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "herdrapi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "socket")
}

func readProtocolRequest(conn net.Conn) (protocolRequest, error) {
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return protocolRequest{}, err
	}
	var request protocolRequest
	if err := json.Unmarshal([]byte(line), &request); err != nil {
		return protocolRequest{}, err
	}
	return request, nil
}

func writeResponse(conn net.Conn, id string, result json.RawMessage) error {
	response := struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
	}{ID: id, Result: result}
	return json.NewEncoder(conn).Encode(response)
}
