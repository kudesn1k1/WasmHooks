package cluster

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/cluster/wire"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

// OutHeader identifies an attempt for which gRPC assigned an HTTP/2
// stream. A dial failure before that point could never start a script.
type dispatchKey struct{}
type dispatchStats struct{}

func (dispatchStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context   { return ctx }
func (dispatchStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (dispatchStats) HandleConn(context.Context, stats.ConnStats)                       {}
func (dispatchStats) HandleRPC(ctx context.Context, event stats.RPCStats) {
	if _, ok := event.(*stats.OutHeader); ok {
		if sent, ok := ctx.Value(dispatchKey{}).(*atomic.Bool); ok {
			sent.Store(true)
		}
	}
}

func unavailable(reason string) execproto.ExecuteResult {
	return execproto.ExecuteResult{Outcome: execproto.OutcomeUnavailable, Reason: reason}
}

func retryable(res execproto.ExecuteResult) bool {
	return res.Outcome == execproto.OutcomeUnavailable && (res.Reason == execproto.ReasonOverloaded || res.Reason == execproto.ReasonPoolSaturated)
}

func guestResult(res execproto.ExecuteResult) bool {
	switch res.Outcome {
	case execproto.OutcomeOK, execproto.OutcomeTimeout:
		return true
	case execproto.OutcomeHandlerError:
		return res.Reason != execproto.ReasonInvalidModule
	}
	return false
}

func (c *Cluster) acquire(id string, retry bool) *peer {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.members[id]
	if p != nil {
		p.active++
		c.stats.Attempts[id]++
		if retry {
			c.stats.Retries++
		}
	}
	return p
}

func (c *Cluster) release(p *peer, successAfterRetry bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if successAfterRetry {
		c.stats.SuccessfulRetries++
	}
	p.active--
	if p.active == 0 && p.retired {
		p.conn.Close()
	}
}

func (c *Cluster) Execute(ctx context.Context, req execproto.ExecuteRequest) (execproto.ExecuteResult, error) {
	request := &wire.ExecuteRequest{
		TenantId: req.TenantID, Hook: req.Hook, HookDefVersion: req.HookDefVersion,
		ConfigVersion: req.ConfigVersion, ModuleHash: req.ModuleHash,
		Payload: req.Payload, IdempotencyKey: req.IdempotencyKey,
	}
	owners := c.Owners(req.TenantID, req.Hook)
	last := unavailable("no_healthy_executor")
	var uncertain error
	attempts := 0
	for _, id := range owners {
		if attempts == 2 {
			break
		}
		if ctx.Err() != nil {
			if attempts == 0 {
				reason := execproto.ReasonDeadline
				if errors.Is(ctx.Err(), context.Canceled) {
					reason = execproto.ReasonCallerCanceled
				}
				last = unavailable(reason)
			}
			break
		}
		retry := attempts != 0
		if deadline, ok := ctx.Deadline(); retry && ok && time.Until(deadline) < c.opts.RetryMinBudget {
			break
		}
		p := c.acquire(id, retry)
		if p == nil {
			continue
		}
		attempts++
		var sent atomic.Bool
		call := context.WithValue(c.authenticated(ctx), dispatchKey{}, &sent)
		reply, err := p.client.Execute(call, request)
		c.release(p, retry && err == nil && reply.Outcome == string(execproto.OutcomeOK))
		if err != nil {
			if status.Code(err) == codes.Unauthenticated {
				last = unavailable("executor_auth")
				break // server refused the request before execution
			}
			last = unavailable("executor_unreachable")
			if sent.Load() {
				uncertain = err
			}
			continue
		}
		last = decodeReply(reply)
		if last.Outcome == execproto.OutcomeUnavailable && last.ExecNanos > 0 {
			// Older executors may report caller_canceled as unavailable after
			// the guest started. Its result is unknown, and it must not run again.
			return execproto.ExecuteResult{}, errors.New("executor returned unavailable after script execution started")
		}
		if guestResult(last) {
			return last, nil
		}
		if !retryable(last) {
			break
		}
	}
	// A later confirmed rejection cannot establish what happened on an
	// earlier dispatched attempt with a lost response.
	if uncertain != nil {
		return execproto.ExecuteResult{}, uncertain
	}
	return last, nil
}

func decodeReply(r *wire.ExecuteReply) execproto.ExecuteResult {
	res := execproto.ExecuteResult{
		Outcome: execproto.Outcome(r.Outcome), Reason: r.Reason, Result: r.Result,
		Error: r.Error, ExecNanos: r.ExecNanos, ColdStart: r.ColdStart, Logs: r.Logs,
	}
	for _, e := range r.Effects {
		res.Effects = append(res.Effects, execproto.Effect{Type: e.Type, Key: e.Key, Payload: e.Payload})
	}
	return res
}
