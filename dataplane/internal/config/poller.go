package config

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"
)

// Poller keeps a Store in sync with the control plane: one long-poll after
// another, the next one starting as soon as the previous answers. On any
// failure it keeps the snapshot it has and retries with exponential backoff
// and full jitter, so a control plane outage never stops calls, and a fleet
// of data planes does not hammer a control plane that is coming back up.
type Poller struct {
	Source     *HTTPSource
	Store      *Store
	OnUpdate   func(*Snapshot) // called after each applied snapshot; must not block
	Log        *slog.Logger
	MinBackoff time.Duration  // 0 means 500ms
	MaxBackoff time.Duration  // 0 means 10s
	Rand       func() float64 // nil means math/rand/v2.Float64
}

// Run polls until ctx is done and returns ctx.Err().
func (p *Poller) Run(ctx context.Context) error {
	after := int64(-1)
	if cur := p.Store.Current(); cur != nil {
		after = cur.Version()
	}
	failures := 0
	for {
		snap, err := p.Source.Fetch(ctx, after)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && snap != nil {
			err = p.Store.Update(snap)
			switch {
			case errors.Is(err, ErrStaleSnapshot):
				// Not newer than what we have: nothing to apply. The next
				// long-poll asks for anything newer than our version.
				err = nil
			case err == nil:
				after = snap.Version
				p.Log.Info("snapshot applied", "version", snap.Version, "hooks", len(snap.Hooks), "bindings", len(snap.Bindings))
				if p.OnUpdate != nil {
					p.OnUpdate(snap)
				}
			}
		}
		if err == nil {
			failures = 0
			continue // success and 304 both go straight into the next long-poll
		}

		failures++
		wait := p.backoff(failures)
		if errors.Is(err, ErrUnauthorized) {
			p.Log.Error("control plane rejected the internal token; check WASMHOOKS_INTERNAL_TOKEN", "err", err, "retry_in", wait)
		} else {
			p.Log.Error("snapshot rejected or control plane unreachable; keeping the current snapshot", "err", err, "retry_in", wait)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// backoff is exponential with full jitter: uniform in [0, min(max, min*2^(n-1))].
func (p *Poller) backoff(failures int) time.Duration {
	lo, hi := p.MinBackoff, p.MaxBackoff
	if lo <= 0 {
		lo = 500 * time.Millisecond
	}
	if hi <= 0 {
		hi = 10 * time.Second
	}
	ceiling := lo << min(failures-1, 20)
	if ceiling <= 0 || ceiling > hi {
		ceiling = hi
	}
	r := p.Rand
	if r == nil {
		r = rand.Float64
	}
	return time.Duration(r() * float64(ceiling))
}
