package notify

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultDeliveryTimeout bounds a single Deliver call during Dispatch.
const DefaultDeliveryTimeout = 30 * time.Second

// DispatchOptions tunes Dispatch behavior.
type DispatchOptions struct {
	// DeliveryTimeout bounds each Deliver call. Zero uses
	// DefaultDeliveryTimeout.
	DeliveryTimeout time.Duration
}

// Dispatch delivers every subscribed payload to every channel. Channels run
// concurrently (one hung endpoint cannot starve the others); within a channel,
// payloads deliver in order so stateful channels (Slack message upserts) stay
// consistent. Each delivery is bounded by a timeout; a channel that overruns it
// is abandoned for the rest of the batch. Errors are collected, never fatal.
func Dispatch(ctx context.Context, channels []Channel, payloads []Payload) []error {
	return DispatchWith(ctx, channels, payloads, DispatchOptions{})
}

// DispatchWith is Dispatch with explicit options.
func DispatchWith(ctx context.Context, channels []Channel, payloads []Payload, opts DispatchOptions) []error {
	timeout := opts.DeliveryTimeout
	if timeout <= 0 {
		timeout = DefaultDeliveryTimeout
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	record := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	for _, s := range channels {
		wg.Add(1)
		go func(s Channel) {
			defer wg.Done()
			subs := s.Subscribes()
			// A channel that opts into grouping (Grouper) has its drift
			// payloads batched here, per channel, so different channels can
			// group differently without the producer knowing. Non-Groupers get
			// the ungrouped per-stack list unchanged.
			chPayloads := payloads
			if g, ok := s.(Grouper); ok {
				chPayloads = GroupPayloads(payloads, g.GroupingMode())
			}
			for _, p := range chPayloads {
				if !subscribed(subs, p.Event) {
					continue
				}
				if err := ctx.Err(); err != nil {
					record(fmt.Errorf("channel %s: %w", s.Name(), err))
					return
				}
				err, timedOut := deliverBounded(ctx, s, p, timeout)
				if timedOut {
					// The Deliver goroutine may still be running (a channel
					// that ignores ctx); abandon this channel rather than
					// racing further deliveries against it.
					record(fmt.Errorf("channel %s: event %s: delivery timed out after %s (skipping remaining deliveries)", s.Name(), p.Event, timeout))
					return
				}
				if err != nil {
					record(fmt.Errorf("channel %s: event %s: %w", s.Name(), p.Event, err))
				}
			}
		}(s)
	}
	wg.Wait()
	return errs
}

// deliverBounded runs Deliver under a per-delivery timeout. timedOut is true
// only when the channel failed to return by the deadline (i.e. it also ignored
// ctx cancellation for the grace we give it).
func deliverBounded(ctx context.Context, s Channel, p Payload, timeout time.Duration) (err error, timedOut bool) {
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.Deliver(dctx, p)
	}()
	select {
	case err := <-done:
		return err, false
	case <-dctx.Done():
		// Grace period: a well-behaved channel returns promptly once its ctx
		// is cancelled - prefer its real error over a bare timeout.
		select {
		case err := <-done:
			return err, false
		case <-time.After(2 * time.Second):
			return nil, true
		}
	}
}

func subscribed(subs []Event, ev Event) bool {
	for _, w := range subs {
		if w == ev {
			return true
		}
	}
	return false
}
