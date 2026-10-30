package hcclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Subscription owns a dedicated connection. A loss requires a new snapshot,
// never silent sequence continuation. The caller reconciles on missed events.
type Subscription struct {
	Snapshot SubscribeResult
	conn     *connection
}

func (c *Client) Subscribe(ctx context.Context, p SubscribeParams) (*Subscription, error) {
	n, _, err := c.open(ctx, "events.subscribe")
	if err != nil {
		return nil, err
	}
	var snapshot SubscribeResult
	if _, err = n.exchange("events.subscribe", p, 2, &snapshot); err != nil {
		n.close()
		return nil, err
	}
	// The handshake is bounded; the stream lives until the supplied context ends.
	n.stop()
	n.cancel()
	n.cancel = func() {}
	n.stop = context.AfterFunc(ctx, func() { n.Conn.Close() })
	deadline, _ := ctx.Deadline()
	if deadline.IsZero() {
		deadline = time.Time{}
	}
	if err = n.SetDeadline(deadline); err != nil {
		n.close()
		return nil, err
	}
	return &Subscription{Snapshot: snapshot, conn: n}, nil
}
func (s *Subscription) Close() error { s.conn.close(); return nil }
func (s *Subscription) Next() (Notification, error) {
	line, err := s.conn.line()
	if err != nil {
		return Notification{}, err
	}
	var v wireResponse
	if err = json.Unmarshal(line, &v); err != nil {
		return Notification{}, err
	}
	if v.JSONRPC != "2.0" || len(v.ID) > 0 || len(v.Params) == 0 {
		return Notification{}, fmt.Errorf("invalid HC notification")
	}
	switch v.Method {
	case "event", "heartbeat", "missed":
		return Notification{Method: v.Method, Params: v.Params}, nil
	default:
		return Notification{}, fmt.Errorf("unexpected HC notification %q", v.Method)
	}
}
