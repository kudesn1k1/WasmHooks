// Package cluster connects gateway and executor processes through the
// versioned gRPC contract in api/executor.proto.
package cluster

import (
	"context"
	"crypto/subtle"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/cluster/wire"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const messageLimit = 8 << 20

type executorServer struct {
	wire.UnimplementedExecutorServer
	exec  execproto.Executor
	id    string
	ready func() bool
}

// NewServer serves the internal Execute and Health methods. Readiness is
// evaluated per request so a terminating pod can decline new work.
func NewServer(exec execproto.Executor, id, token string, ready func() bool) *grpc.Server {
	auth := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		values := metadata.ValueFromIncomingContext(ctx, "authorization")
		if token == "" || len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte("Bearer "+token)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid internal credential")
		}
		return handler(ctx, req)
	}
	server := grpc.NewServer(grpc.UnaryInterceptor(auth), grpc.MaxRecvMsgSize(messageLimit), grpc.MaxSendMsgSize(messageLimit))
	wire.RegisterExecutorServer(server, &executorServer{exec: exec, id: id, ready: ready})
	return server
}

func (s *executorServer) Health(context.Context, *wire.HealthRequest) (*wire.HealthReply, error) {
	return &wire.HealthReply{ExecutorId: s.id, Ready: s.ready()}, nil
}

func (s *executorServer) Execute(ctx context.Context, r *wire.ExecuteRequest) (*wire.ExecuteReply, error) {
	if !s.ready() {
		return &wire.ExecuteReply{Outcome: string(execproto.OutcomeUnavailable), Reason: "executor_not_ready"}, nil
	}
	result, err := s.exec.Execute(ctx, execproto.ExecuteRequest{
		TenantID: r.TenantId, Hook: r.Hook, HookDefVersion: r.HookDefVersion, ConfigVersion: r.ConfigVersion,
		ModuleHash: r.ModuleHash, IdempotencyKey: r.IdempotencyKey, Payload: r.Payload,
	})
	if err != nil {
		return nil, status.Error(codes.Unavailable, "executor returned no result")
	}
	resp := &wire.ExecuteReply{Outcome: string(result.Outcome), Reason: result.Reason, Result: result.Result,
		Error: result.Error, ExecNanos: result.ExecNanos, ColdStart: result.ColdStart, Logs: result.Logs,
	}
	for _, effect := range result.Effects {
		resp.Effects = append(resp.Effects, &wire.Effect{Type: effect.Type, Key: effect.Key, Payload: effect.Payload})
	}
	return resp, nil
}
