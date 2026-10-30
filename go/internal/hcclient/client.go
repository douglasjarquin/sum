package hcclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
)

type Effect string

const (
	NotSubmitted Effect = "not-submitted"
	Refused      Effect = "refused"
	Uncertain    Effect = "uncertain"
)

// Error carries effect certainty independently from the native diagnostic.
type Error struct {
	Effect Effect
	Method string
	RPC    *RPCError
	Cause  error
}

func (e *Error) Error() string { return fmt.Sprintf("HC %s (%s): %v", e.Method, e.Effect, e.Cause) }
func (e *Error) Unwrap() error { return e.Cause }

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("RPC %d: %s (%s)", e.Code, e.Message, e.Data) }

// Client never starts a core, reconnects an operation, or replays an effect.
// Each ordinary call performs hello on its own existing-socket connection.
type Client struct {
	Socket           string
	CoreID           string
	Timeout          time.Duration
	MaxResponseBytes int
}
type wireResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type connection struct {
	net.Conn
	reader *bufio.Reader
	limit  int
	stop   func() bool
	cancel context.CancelFunc
}

func (c *connection) close() { c.stop(); c.cancel(); c.Conn.Close() }
func (c *Client) connect(ctx context.Context) (*connection, error) {
	if c.Socket == "" {
		return nil, fmt.Errorf("explicit existing socket required")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	n, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		cancel()
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	if err = n.SetDeadline(deadline); err != nil {
		n.Close()
		cancel()
		return nil, err
	}
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = 2 << 20
	}
	return &connection{Conn: n, reader: bufio.NewReader(n), limit: limit, stop: context.AfterFunc(ctx, func() { n.Close() }), cancel: cancel}, nil
}
func (c *connection) line() ([]byte, error) {
	var out []byte
	for {
		part, err := c.reader.ReadSlice('\n')
		if len(out)+len(part) > c.limit {
			return nil, fmt.Errorf("response exceeds %d bytes", c.limit)
		}
		out = append(out, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}
func (c *connection) exchange(method string, params any, id int, out any) (bool, error) {
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	// From the first write attempt onward, transport errors cannot prove no effect.
	for len(data) > 0 {
		n, e := c.Write(data)
		if e != nil {
			return true, e
		}
		if n == 0 {
			return true, io.ErrShortWrite
		}
		data = data[n:]
	}
	line, err := c.line()
	if err != nil {
		return true, err
	}
	var response wireResponse
	if err = json.Unmarshal(line, &response); err != nil {
		return true, err
	}
	expected, _ := json.Marshal(id)
	if response.JSONRPC != "2.0" || !bytes.Equal(response.ID, expected) || response.Method != "" || (response.Error == nil) == (len(response.Result) == 0) {
		return true, fmt.Errorf("invalid response envelope for request %d", id)
	}
	if response.Error != nil {
		return true, response.Error
	}
	if bytes.Equal(response.Result, []byte("null")) {
		return true, fmt.Errorf("unexpected null result")
	}
	if err := validateResult(method, response.Result); err != nil {
		return true, err
	}
	return true, json.Unmarshal(response.Result, out)
}
func (c *Client) open(ctx context.Context, method string) (*connection, HelloResult, error) {
	var h HelloResult
	if c.CoreID == "" {
		return nil, h, fmt.Errorf("expected permanent core ID required")
	}
	n, err := c.connect(ctx)
	if err != nil {
		return nil, h, err
	}
	if _, err = n.exchange("hello", map[string]int{"generation": contract.HCGeneration}, 1, &h); err != nil {
		n.close()
		return nil, h, err
	}
	if h.Generation != contract.HCGeneration || h.Core.ID != c.CoreID || h.Version != contract.HCVersion || (method != "hello" && !slices.Contains(h.Methods, method)) {
		n.close()
		return nil, h, fmt.Errorf("core identity, pinned version or method mismatch")
	}
	if (method == "terminal.send" || method == "terminal.get") && !slices.Contains(h.Features, "status") {
		n.close()
		return nil, h, fmt.Errorf("required native status feature unavailable")
	}
	if method == "events.subscribe" && !slices.Contains(h.Features, "events") {
		n.close()
		return nil, h, fmt.Errorf("required native event feature unavailable")
	}
	return n, h, nil
}
func (c *Client) call(ctx context.Context, method string, params, out any, mutates bool) error {
	n, _, err := c.open(ctx, method)
	if err != nil {
		return &Error{Effect: NotSubmitted, Method: method, Cause: err}
	}
	defer n.close()
	written, err := n.exchange(method, params, 2, out)
	if err == nil {
		return nil
	}
	effect := NotSubmitted
	if mutates && written {
		effect = Uncertain
	}
	var rpc *RPCError
	// Only native stable pre-input refusals are eligible for a safe retry.
	if errors.As(err, &rpc) && method == "terminal.send" && knownSendRefusal(rpc) {
		effect = Refused
	}
	return &Error{Effect: effect, Method: method, RPC: rpc, Cause: err}
}
func knownSendRefusal(e *RPCError) bool {
	var d struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(e.Data, &d) != nil {
		return false
	}
	switch d.Kind {
	case "blocked", "status_mismatch", "not_found", "finished", "invalid_params", "config":
		return true
	}
	return false
}
func (c *Client) Hello(ctx context.Context) (HelloResult, error) {
	n, h, err := c.open(ctx, "hello")
	if err == nil {
		n.close()
	}
	return h, err
}
func (c *Client) Run(ctx context.Context, p RunParams) (v RunResult, err error) {
	err = c.call(ctx, "terminal.run", p, &v, true)
	return
}
func (c *Client) Get(ctx context.Context, address string) (v Terminal, err error) {
	err = c.call(ctx, "terminal.get", map[string]string{"terminal": address}, &v, false)
	return
}
func (c *Client) List(ctx context.Context, p ListParams) (v ListResult, err error) {
	err = c.call(ctx, "terminal.list", p, &v, false)
	return
}
func (c *Client) Send(ctx context.Context, p SendParams) (v SendResult, err error) {
	if p.If != "" && p.If != "idle" && p.If != "working" {
		return v, &Error{Effect: NotSubmitted, Method: "terminal.send", Cause: fmt.Errorf("unsupported native input guard")}
	}
	err = c.call(ctx, "terminal.send", p, &v, true)
	return
}
func (c *Client) Read(ctx context.Context, address string, lines uint) (v ReadResult, err error) {
	err = c.call(ctx, "terminal.read", map[string]any{"terminal": address, "lines": lines}, &v, false)
	return
}
func (c *Client) Wait(ctx context.Context, p WaitParams) (v WaitResult, err error) {
	err = c.call(ctx, "terminal.wait", p, &v, false)
	return
}
func (c *Client) Interrupt(ctx context.Context, address string) (v InterruptResult, err error) {
	err = c.call(ctx, "terminal.interrupt", map[string]string{"terminal": address}, &v, true)
	return
}
func (c *Client) Kill(ctx context.Context, address string) (v Terminal, err error) {
	err = c.call(ctx, "terminal.kill", map[string]string{"terminal": address}, &v, true)
	return
}
func (c *Client) Remove(ctx context.Context, address string) (v RemoveResult, err error) {
	err = c.call(ctx, "terminal.rm", map[string]string{"terminal": address}, &v, true)
	return
}
func (c *Client) Label(ctx context.Context, address string, labels map[string]string) (v Terminal, err error) {
	err = c.call(ctx, "terminal.label", map[string]any{"terminal": address, "labels": labels}, &v, true)
	return
}

func validateResult(method string, raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	var required []string
	switch method {
	case "hello":
		required = []string{"generation", "version", "core", "framed", "methods", "features"}
	case "terminal.run", "terminal.get", "terminal.kill", "terminal.label":
		required = []string{"address", "labels", "argv", "cwd", "state", "created_ms", "screen", "cols", "rows"}
	case "terminal.send":
		required = []string{"turn"}
	case "terminal.list":
		required = []string{"terminals", "hidden_finished"}
	case "terminal.read":
		required = []string{"text", "complete", "how"}
	case "terminal.wait":
		required = []string{"terminal", "ended", "reason", "turn", "children"}
	case "terminal.interrupt":
		required = []string{"outcome", "key"}
	case "terminal.rm":
		required = []string{"removed"}
	case "templates.list":
		required = []string{"templates"}
	case "events.subscribe":
		required = []string{"seq", "terminals"}
	}
	for _, key := range required {
		if v, ok := object[key]; !ok || bytes.Equal(v, []byte("null")) {
			return fmt.Errorf("%s result missing required %s", method, key)
		}
	}
	if method == "terminal.run" {
		if first, ok := object["first_message"]; ok && !bytes.Equal(first, []byte("null")) {
			var outcome struct {
				Sent *bool `json:"sent"`
			}
			if err := json.Unmarshal(first, &outcome); err != nil {
				return err
			}
			if outcome.Sent == nil {
				return fmt.Errorf("native first_message missing required boolean sent")
			}
		}
	}
	return nil
}

// Inspect discovers identity on an explicitly selected existing socket for
// operator bootstrap. It grants no endpoint or ledger authority. Effectful
// clients must still be constructed with the saved, expected permanent ID.
func Inspect(ctx context.Context, socket string) (HelloResult, error) {
	c := Client{Socket: socket}
	n, err := c.connect(ctx)
	var h HelloResult
	if err != nil {
		return h, err
	}
	defer n.close()
	if _, err = n.exchange("hello", map[string]int{"generation": contract.HCGeneration}, 1, &h); err != nil {
		return h, err
	}
	if h.Generation != contract.HCGeneration || h.Version != contract.HCVersion || h.Core.ID == "" {
		return h, fmt.Errorf("unsupported or unidentified HC core")
	}
	return h, nil
}

func (c *Client) Templates(ctx context.Context) (v TemplatesResult, err error) {
	err = c.call(ctx, "templates.list", struct{}{}, &v, false)
	return
}
