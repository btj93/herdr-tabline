package herdrapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/herdrapi"
)

func TestUnixClientSubscribeAcknowledgesThenStreamsEventsOnDedicatedConnection(t *testing.T) {
	listener, socket := newUnixListener(t)
	subscriptionReady := make(chan struct{})
	holdStream := make(chan struct{})
	serverErrors := make(chan error, 2)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.Close()
		req, err := readProtocolRequest(conn)
		if err != nil {
			serverErrors <- err
			return
		}
		if req.Method != "events.subscribe" || string(req.Params) != `{"subscriptions":[{"type":"tab.moved"},{"type":"tab.created"}]}` {
			serverErrors <- fmt.Errorf("subscription request = %s %s", req.Method, req.Params)
			return
		}
		if err := writeResponse(conn, req.ID, json.RawMessage(`{"type":"subscription_started"}`)); err != nil {
			serverErrors <- err
			return
		}
		if _, err := conn.Write([]byte(`{"event":"tab_moved","data":{"tab_id":"tab-2","insert_index":0}}` + "\n")); err != nil {
			serverErrors <- err
			return
		}
		close(subscriptionReady)
		<-holdStream
		serverErrors <- nil
	}()

	client := herdrapi.NewUnixClient(socket)
	ctx, cancel := newStreamContext(t)
	events, err := client.Subscribe(ctx, []string{"tab.moved", "tab.created"})
	if err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, subscriptionReady, "subscription acknowledgement and first event")
	select {
	case event := <-events:
		if event.Kind != "tab_moved" || string(event.Data) != `{"tab_id":"tab-2","insert_index":0}` {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(testTimeout):
		t.Fatal("event was not delivered")
	}

	renameDone := make(chan error, 1)
	go func() { renameDone <- client.RenameTab(context.Background(), "tab-2", "renamed") }()
	conn := acceptWithin(t, listener, "the rename connection")
	req, err := readProtocolRequest(conn)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "tab.rename" {
		t.Fatalf("second connection method = %q, want tab.rename", req.Method)
	}
	if err := writeResponse(conn, req.ID, json.RawMessage(`{"type":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := awaitError(t, renameDone, "RenameTab past a stalled event stream"); err != nil {
		t.Fatal(err)
	}

	// teardownWindow rather than testTimeout: the watchdog on the stream context is armed
	// for testTimeout, so only a tight window proves cancel() is what closed the channel.
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("event channel remained open after cancellation")
		}
	case <-time.After(teardownWindow):
		t.Fatal("event channel did not close after cancellation")
	}
	close(holdStream)
	if err := awaitError(t, serverErrors, "the subscription server goroutine"); err != nil {
		t.Fatal(err)
	}
	closeWithin(t, client)
}

func TestUnixClientSubscribeRejectsMissingOrMalformedAcknowledgement(t *testing.T) {
	tests := []struct {
		name     string
		response func(protocolRequest) string
		contains string
	}{
		{name: "missing acknowledgement", response: func(req protocolRequest) string { return fmt.Sprintf(`{"id":%q,"result":{}}`, req.ID) + "\n" }, contains: "subscription_started"},
		{name: "malformed acknowledgement", response: func(protocolRequest) string { return "not-json\n" }, contains: "decode acknowledgement"},
		{name: "mismatched ID", response: func(protocolRequest) string { return `{"id":"wrong","result":{"type":"subscription_started"}}` + "\n" }, contains: "response id"},
		{name: "RPC error acknowledgement", response: func(req protocolRequest) string {
			return fmt.Sprintf(`{"id":%q,"error":{"code":"unsupported_subscription","message":"unknown type"}}`, req.ID) + "\n"
		}, contains: "rpc error unsupported_subscription: unknown type"},
		{name: "acknowledgement without outcome", response: func(req protocolRequest) string {
			return fmt.Sprintf(`{"id":%q}`, req.ID) + "\n"
		}, contains: "neither result nor error"},
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
			_, err := client.Subscribe(testContext(t), []string{"tab.moved"})
			if err == nil || !strings.Contains(err.Error(), test.contains) || !strings.Contains(err.Error(), "events.subscribe") || !strings.Contains(err.Error(), socket) {
				t.Fatalf("error = %v, want method, socket, and %q", err, test.contains)
			}
		})
	}
}

// TestUnixClientSubscribeClosesEventChannelOnTerminalStreamConditions pins the
// contract that a dead stream is reported by closing the channel, so a caller
// can tell a terminated subscription apart from a quiet one.
func TestUnixClientSubscribeClosesEventChannelOnTerminalStreamConditions(t *testing.T) {
	// holdOpen keeps the peer connected after the bad frame is written. Without it the
	// server's own disconnect closes the channel through the end-of-stream path, and the
	// case would pass even if the condition it names were ignored entirely.
	tests := []struct {
		name     string
		after    func(net.Conn)
		holdOpen bool
	}{
		{name: "peer end of stream", after: func(net.Conn) {}},
		{name: "malformed event frame", holdOpen: true, after: func(conn net.Conn) {
			_, _ = conn.Write([]byte("not-json\n"))
		}},
		{name: "event without kind", holdOpen: true, after: func(conn net.Conn) {
			_, _ = conn.Write([]byte(`{"data":{"tab_id":"tab-2"}}` + "\n"))
		}},
		{name: "empty event kind", holdOpen: true, after: func(conn net.Conn) {
			_, _ = conn.Write([]byte(`{"event":"","data":{"tab_id":"tab-2"}}` + "\n"))
		}},
		{name: "oversized event frame", holdOpen: true, after: func(conn net.Conn) {
			_, _ = conn.Write(bytes.Repeat([]byte("x"), testMaxFrameBytes+1024))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener, socket := newUnixListener(t)
			finished := make(chan struct{})
			t.Cleanup(func() { close(finished) })
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
				if err := writeResponse(conn, req.ID, json.RawMessage(`{"type":"subscription_started"}`)); err != nil {
					return
				}
				test.after(conn)
				if test.holdOpen {
					<-finished
				}
			}()
			client := herdrapi.NewUnixClient(socket)
			t.Cleanup(func() { closeWithin(t, client) })
			// Same reasoning as the cancel() and Close() assertions: a deadline-bearing
			// context would close the stream itself, so this would pass even if the
			// terminal condition under test were ignored.
			streamCtx, _ := newStreamContext(t)
			events, err := client.Subscribe(streamCtx, []string{"tab.moved"})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case event, ok := <-events:
				if ok {
					t.Fatalf("terminal stream condition delivered event %#v", event)
				}
			case <-time.After(teardownWindow):
				t.Fatal("event channel did not close on a terminal stream condition")
			}
		})
	}
}

func TestUnixClientCloseClosesEventChannelPromptly(t *testing.T) {
	listener, socket := newUnixListener(t)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := readProtocolRequest(conn)
		if err == nil {
			_ = writeResponse(conn, req.ID, json.RawMessage(`{"type":"subscription_started"}`))
		}
		var one [1]byte
		_, _ = conn.Read(one[:])
	}()
	client := herdrapi.NewUnixClient(socket)
	streamCtx, _ := newStreamContext(t)
	events, err := client.Subscribe(streamCtx, []string{"tab.moved"})
	if err != nil {
		t.Fatal(err)
	}
	// Both windows are teardownWindow, not testTimeout: this test asserts promptness, and
	// only a window far tighter than the stream watchdog attributes the close to Close().
	closed := make(chan struct{})
	go func() { _ = client.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(teardownWindow):
		t.Fatal("Close did not return promptly")
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("event channel remained open after Close")
		}
	case <-time.After(teardownWindow):
		t.Fatal("event channel did not close promptly after Close")
	}
}

func TestUnixClientSubscribePreservesDialFailure(t *testing.T) {
	client := herdrapi.NewUnixClient(newSocketPath(t))
	t.Cleanup(func() { closeWithin(t, client) })
	_, err := client.Subscribe(testContext(t), []string{"tab.moved"})
	if err == nil {
		t.Fatal("Subscribe succeeded without a socket")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("dial failure was masked as context cancellation: %v", err)
	}
}
