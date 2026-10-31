package hookstatus

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
)

func awaitObserverForeground(parent context.Context, client *hcclient.Client, initial hcclient.Terminal) (hcclient.Terminal, error) {
	if initial.Foreground != nil {
		return initial, nil
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	for {
		subscription, err := client.Subscribe(ctx, hcclient.SubscribeParams{Terminal: initial.Address})
		if err != nil {
			if retrySubscription(ctx, err) {
				select {
				case <-ctx.Done():
					return hcclient.Terminal{}, ctx.Err()
				case <-time.After(100 * time.Millisecond):
					continue
				}
			}
			return hcclient.Terminal{}, fmt.Errorf("observe startup foreground: %w", err)
		}
		terminal, missed, err := observerForegroundSnapshot(ctx, client, initial, subscription)
		subscription.Close()
		if err != nil {
			if retryNativeObservation(ctx, err) || retrySubscription(ctx, err) {
				select {
				case <-ctx.Done():
					return hcclient.Terminal{}, ctx.Err()
				case <-time.After(100 * time.Millisecond):
					continue
				}
			}
			return hcclient.Terminal{}, err
		}
		if !missed {
			return terminal, nil
		}
	}
}
func observerForegroundSnapshot(ctx context.Context, client *hcclient.Client, initial hcclient.Terminal, subscription *hcclient.Subscription) (hcclient.Terminal, bool, error) {
	for {
		terminal, err := client.Get(ctx, initial.Address)
		if err != nil {
			return terminal, false, fmt.Errorf("observe startup program: %w", err)
		}
		if terminal.State != "live" || terminal.Keep || terminal.Address != initial.Address || terminal.CreatedMS != initial.CreatedMS || !reflect.DeepEqual(terminal.PID, initial.PID) || !reflect.DeepEqual(terminal.Argv, initial.Argv) || !reflect.DeepEqual(terminal.Labels, initial.Labels) || terminal.Cwd != initial.Cwd {
			return terminal, false, fmt.Errorf("observer program identity changed before foreground proof; startup was not adopted")
		}
		if terminal.Foreground != nil {
			return terminal, false, nil
		}
		event, err := subscription.Next()
		if err != nil {
			return terminal, false, fmt.Errorf("observer foreground remains unconfirmed: %w", err)
		}
		if event.Method == "missed" {
			return terminal, true, nil
		}
	}
}
