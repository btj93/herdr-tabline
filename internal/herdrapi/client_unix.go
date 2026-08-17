package herdrapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

const maxOrdinaryConnections = 8

// maxFrameBytes bounds a single newline-delimited protocol frame, including its
// terminator. Herdr responses and events are small control payloads, so this
// only ever trips on a malformed or hostile peer; without it a peer that never
// sends a newline can grow this process without bound.
const maxFrameBytes = 1 << 20

var (
	errClientClosed  = errors.New("client is closed")
	errFrameTooLarge = fmt.Errorf("protocol error: frame exceeds %d bytes", maxFrameBytes)
)

type unixClient struct {
	socket string
	slots  chan struct{}
	nextID atomic.Uint64

	closeCtx  context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	closeDone chan struct{}
	wg        sync.WaitGroup
}

type requestEnvelope struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type responseEnvelope struct {
	ID     string
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// UnmarshalJSON decodes a response and enforces the protocol invariant that a
// response carries exactly one outcome. Herdr defines success and error
// responses as disjoint shapes — `{id, result}` and `{id, error}` — so a
// response with neither member is malformed rather than a void success, and a
// response with both is ambiguous. An explicit null error is treated as absent
// so a peer that always emits the member still decodes; the result member is
// detected by presence alone, preserving whatever value the protocol defines.
func (r *responseEnvelope) UnmarshalJSON(data []byte) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	if raw, ok := members["id"]; ok {
		if err := json.Unmarshal(raw, &r.ID); err != nil {
			return fmt.Errorf("id member: %w", err)
		}
	}
	result, hasResult := members["result"]
	failure, hasError := members["error"]
	if hasError && isJSONNull(failure) {
		hasError = false
	}
	switch {
	case hasResult && hasError:
		return errors.New("response carries both result and error")
	case !hasResult && !hasError:
		return errors.New("response carries neither result nor error")
	case hasResult:
		r.Result = result
	default:
		if err := json.Unmarshal(failure, &r.Error); err != nil {
			return fmt.Errorf("error member: %w", err)
		}
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func NewUnixClient(socket string) Client {
	closeCtx, cancel := context.WithCancel(context.Background())
	return &unixClient{
		socket:    socket,
		slots:     make(chan struct{}, maxOrdinaryConnections),
		closeCtx:  closeCtx,
		cancel:    cancel,
		closeDone: make(chan struct{}),
	}
}

func (c *unixClient) Snapshot(ctx context.Context) (Snapshot, error) {
	var snapshot Snapshot
	err := c.call(ctx, "session.snapshot", struct{}{}, &snapshot)
	return snapshot, err
}

func (c *unixClient) ProcessInfo(ctx context.Context, paneID string) (ProcessInfo, error) {
	params := struct {
		PaneID string `json:"pane_id"`
	}{PaneID: paneID}
	var info ProcessInfo
	err := c.call(ctx, "pane.process_info", params, &info)
	return info, err
}

func (c *unixClient) RenameTab(ctx context.Context, tabID, label string) error {
	params := struct {
		TabID string `json:"tab_id"`
		Label string `json:"label"`
	}{TabID: tabID, Label: label}
	return c.call(ctx, "tab.rename", params, nil)
}

func (c *unixClient) call(ctx context.Context, method string, params, out any) error {
	if !c.begin() {
		return c.wrap(method, errClientClosed)
	}
	defer c.wg.Done()

	opCtx, cancel := c.operationContext(ctx)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-opCtx.Done():
		return c.wrap(method, opCtx.Err())
	}

	conn, err := (&net.Dialer{}).DialContext(opCtx, "unix", c.socket)
	if err != nil {
		return c.wrap(method, contextError(opCtx, err))
	}
	defer conn.Close()
	if deadline, ok := opCtx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return c.wrap(method, fmt.Errorf("set deadline: %w", err))
		}
	}
	stopClose := context.AfterFunc(opCtx, func() { _ = conn.Close() })
	defer stopClose()

	id := c.newID()
	if err := json.NewEncoder(conn).Encode(requestEnvelope{ID: id, Method: method, Params: params}); err != nil {
		return c.wrap(method, fmt.Errorf("write request: %w", contextError(opCtx, err)))
	}
	reader := bufio.NewReader(conn)
	line, err := readNewlineFrame(reader)
	if err != nil {
		return c.wrap(method, fmt.Errorf("read response: %w", contextError(opCtx, err)))
	}
	response, err := decodeResponse(line, id, "response")
	if err != nil {
		return c.wrap(method, err)
	}
	if err := requireConnectionEnd(reader, opCtx); err != nil {
		return c.wrap(method, err)
	}
	if response.Error != nil {
		return c.wrap(method, fmt.Errorf("rpc error %s: %s", response.Error.Code, response.Error.Message))
	}
	if out != nil {
		if err := json.Unmarshal(response.Result, out); err != nil {
			return c.wrap(method, fmt.Errorf("decode result: %w", err))
		}
	}
	return nil
}

func (c *unixClient) Subscribe(ctx context.Context, names []string) (<-chan Event, error) {
	const method = "events.subscribe"
	if !c.begin() {
		return nil, c.wrap(method, errClientClosed)
	}
	transferred := false
	defer func() {
		if !transferred {
			c.wg.Done()
		}
	}()

	opCtx, cancel := c.operationContext(ctx)
	conn, err := (&net.Dialer{}).DialContext(opCtx, "unix", c.socket)
	if err != nil {
		err = contextError(opCtx, err)
		cancel()
		return nil, c.wrap(method, err)
	}
	if deadline, ok := opCtx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			cancel()
			return nil, c.wrap(method, fmt.Errorf("set deadline: %w", err))
		}
	}
	stopClose := context.AfterFunc(opCtx, func() { _ = conn.Close() })
	fail := func(err error) (<-chan Event, error) {
		stopClose()
		_ = conn.Close()
		cancel()
		return nil, c.wrap(method, err)
	}

	subscriptions := make([]struct {
		Type string `json:"type"`
	}, len(names))
	for index, name := range names {
		subscriptions[index].Type = name
	}
	params := struct {
		Subscriptions []struct {
			Type string `json:"type"`
		} `json:"subscriptions"`
	}{Subscriptions: subscriptions}
	id := c.newID()
	if err := json.NewEncoder(conn).Encode(requestEnvelope{ID: id, Method: method, Params: params}); err != nil {
		return fail(fmt.Errorf("write request: %w", contextError(opCtx, err)))
	}
	reader := bufio.NewReader(conn)
	line, err := readNewlineFrame(reader)
	if err != nil {
		return fail(fmt.Errorf("read acknowledgement: %w", contextError(opCtx, err)))
	}
	response, err := decodeResponse(line, id, "acknowledgement")
	if err != nil {
		return fail(err)
	}
	if response.Error != nil {
		return fail(fmt.Errorf("rpc error %s: %s", response.Error.Code, response.Error.Message))
	}
	var acknowledgement struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(response.Result, &acknowledgement); err != nil {
		return fail(fmt.Errorf("decode acknowledgement result: %w", err))
	}
	if acknowledgement.Type != "subscription_started" {
		return fail(fmt.Errorf("acknowledgement type %q, want subscription_started", acknowledgement.Type))
	}

	events := make(chan Event)
	transferred = true
	go func() {
		defer c.wg.Done()
		defer close(events)
		defer cancel()
		defer stopClose()
		defer conn.Close()
		for {
			line, err := readNewlineFrame(reader)
			if err != nil {
				return
			}
			var event Event
			if err := json.Unmarshal(line, &event); err != nil || event.Kind == "" {
				return
			}
			select {
			case events <- event:
			case <-opCtx.Done():
				return
			}
		}
	}()
	return events, nil
}

func (c *unixClient) Close() error {
	c.mu.Lock()
	if c.closed {
		done := c.closeDone
		c.mu.Unlock()
		<-done
		return nil
	}
	c.closed = true
	c.cancel()
	c.mu.Unlock()

	c.wg.Wait()
	close(c.closeDone)
	return nil
}

func (c *unixClient) begin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.wg.Add(1)
	return true
}

func (c *unixClient) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	opCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.closeCtx, cancel)
	return opCtx, func() {
		stop()
		cancel()
	}
}

func (c *unixClient) newID() string {
	return fmt.Sprintf("herdr-tabline-%d", c.nextID.Add(1))
}

func (c *unixClient) wrap(method string, err error) error {
	return fmt.Errorf("%s via unix socket %q: %w", method, c.socket, err)
}

// readFrameBytes reads up to and including the next newline, refusing any frame
// larger than maxFrameBytes so an unterminated peer stream cannot exhaust
// memory. It returns whatever bytes were consumed alongside the raw read error
// so callers can distinguish a clean end of stream from a truncated frame.
func readFrameBytes(reader *bufio.Reader) ([]byte, error) {
	var frame []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(frame)+len(chunk) > maxFrameBytes {
			return nil, errFrameTooLarge
		}
		frame = append(frame, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			// The buffer filled with no terminator in sight. Reaching the limit here is
			// already a violation: refuse now rather than waiting for the next buffer to
			// fill, which a peer that stalls after an oversized frame would never send.
			if len(frame) >= maxFrameBytes {
				return nil, errFrameTooLarge
			}
			continue
		}
		return frame, err
	}
}

func readNewlineFrame(reader *bufio.Reader) ([]byte, error) {
	line, err := readFrameBytes(reader)
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return nil, errors.New("response is missing newline terminator")
		}
		return nil, err
	}
	return line, nil
}

func decodeResponse(line []byte, id, name string) (responseEnvelope, error) {
	var response responseEnvelope
	if err := json.Unmarshal(line, &response); err != nil {
		return response, fmt.Errorf("decode %s: %w", name, err)
	}
	if response.ID != id {
		return response, fmt.Errorf("response id %q does not match request id %q", response.ID, id)
	}
	return response, nil
}

func requireConnectionEnd(reader *bufio.Reader, ctx context.Context) error {
	extra, err := readFrameBytes(reader)
	if len(extra) != 0 {
		return errors.New("protocol error: extra response line")
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read connection end: %w", contextError(ctx, err))
	}
	return errors.New("protocol error: extra response line")
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
