package hookstatus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
)

// Only read transport failures reconnect. Durable records, incompatible cores
// and uncertain mutations retain their own explicit failure contracts.
func retryNativeObservation(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var native *hcclient.Error
	if !errors.As(err, &native) {
		return false
	}
	switch native.Method {
	case "terminal.list", "terminal.get", "terminal.read", "events.subscribe", "hello":
	default:
		return false
	}
	if native.RPC != nil {
		return unavailableRPC(native.RPC)
	}
	return retryTransport(native.Cause)
}
func retrySubscription(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var native *hcclient.Error
	if errors.As(err, &native) {
		return retryNativeObservation(ctx, err)
	}
	var rpc *hcclient.RPCError
	if errors.As(err, &rpc) {
		return unavailableRPC(rpc)
	}
	return retryTransport(err)
}
func unavailableRPC(rpc *hcclient.RPCError) bool {
	var data struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(rpc.Data, &data) == nil && data.Kind == "unavailable"
}
func retryTransport(err error) bool {
	var network net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network)
}
