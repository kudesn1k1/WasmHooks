package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/cluster"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/config"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/execproto"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/gateway/httpapi"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/internalapi"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/schema"
)

func serveRoles(parent context.Context, f flags, cfg *config.Store, log *slog.Logger, stdout io.Writer) (err error) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	var workers sync.WaitGroup
	var ready atomic.Bool
	// The channel coalesces config and membership changes during compilation.
	updates := make(chan struct{}, 1)
	changed := func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
	schemas := schema.NewCache()
	var engine *localEngine
	if f.role != "gateway" {
		engine, err = newEngine(f, cfg, schemas, log)
		if err != nil {
			cancel()
			return err
		}
	}
	var peers *cluster.Cluster
	if f.role != "combined" {
		peers, err = cluster.New(cluster.Options{
			Address: f.discovery, Token: f.internalToken, Interval: f.discoveryInterval,
			HealthTimeout: f.healthTimeout, RetryMinBudget: f.retryBudget, OnChange: changed,
		})
		if err != nil {
			cancel()
			if engine != nil {
				err = errors.Join(err, engine.close(context.Background()))
			}
			return err
		}
	}
	stopped := make(chan error, 3)
	var shutdown []func(context.Context) error
	defer func() {
		ready.Store(false)
		grace, finish := context.WithTimeout(context.Background(), config.MaxTimeoutMS*time.Millisecond+5*time.Second)
		defer finish()
		// Drain serving calls before closing connections or the Wasm runtime.
		for _, closeServer := range shutdown {
			err = errors.Join(err, closeServer(grace))
		}
		cancel()
		workers.Wait()
		if engine != nil {
			err = errors.Join(err, engine.close(grace))
		}
	}()
	startHTTP := func(addr, label string, handler http.Handler) error {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 65 * time.Second}
		shutdown = append(shutdown, func(ctx context.Context) error {
			if err := srv.Shutdown(ctx); err != nil {
				_ = srv.Close()
				return err
			}
			return nil
		})
		fmt.Fprintf(stdout, "%s%s\n", label, ln.Addr())
		go func() { stopped <- srv.Serve(ln) }()
		return nil
	}
	var public, internal http.Handler
	if f.role == "executor" {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
			if !ready.Load() {
				http.Error(w, "waiting for snapshot", 503)
			}
		})
		public = mux
	} else {
		var target execproto.Executor
		if peers != nil {
			target = peers
		} else {
			target = engine.Executor
		}
		options := gateway.Options{
			MaxInFlight: f.maxInFlight, BreakerThreshold: f.breakerThreshold,
			BreakerCooldown: f.breakerCooldown, DisableBreaker: f.breakerThreshold == 0,
		}
		gw := gateway.New(cfg, target, schemas, options)
		public = httpapi.New(gw, httpapi.Options{Ready: func() bool { return ready.Load() && (peers == nil || len(peers.Members()) > 0) }})
		if peers != nil {
			internal = diagnostics(f, peers, gw)
		}
	}
	if err = startHTTP(f.listen, "listening on ", public); err != nil {
		return err
	}
	if engine != nil && f.internalToken != "" {
		internal = internalapi.New(engine.Executor, f.internalToken, log)
	}
	if internal != nil {
		if err = startHTTP(f.internalListen, "internal listening on ", internal); err != nil {
			return err
		}
	}
	if f.role == "executor" {
		ln, err := net.Listen("tcp", f.rpcListen)
		if err != nil {
			return err
		}
		rpc := cluster.NewServer(engine.Executor, f.advertised, f.internalToken, ready.Load)
		shutdown = append(shutdown, func(ctx context.Context) error {
			done := make(chan struct{})
			go func() { rpc.GracefulStop(); close(done) }()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				rpc.Stop()
				<-done
				return ctx.Err()
			}
		})
		go func() { stopped <- rpc.Serve(ln) }()
	}
	if peers != nil {
		workers.Go(func() { peers.Run(ctx) })
	}
	if f.controlPlaneURL != "" {
		poller := config.Poller{
			Source: &config.HTTPSource{BaseURL: f.controlPlaneURL, Token: f.internalToken},
			Store:  cfg, OnUpdate: func(*config.Snapshot) { changed() }, Log: log,
		}
		workers.Go(func() { _ = poller.Run(ctx) })
	}
	changed()
	for {
		select {
		case <-parent.Done():
			return nil
		case err := <-stopped:
			return err
		case <-updates:
			if cfg.Current() == nil {
				continue
			}
			if engine != nil {
				if peers == nil {
					err = engine.Preload(parent)
				} else {
					err = engine.PreloadOwned(parent, func(b config.Binding) bool {
						owners := peers.Owners(b.TenantID, b.Hook)
						return len(owners) > 0 && owners[0] == f.advertised
					}, func(b config.Binding) {
						log.Info("module preloaded", "tenant_id", b.TenantID, "hook", b.Hook, "module_hash", b.ModuleHash, "executor_id", f.executorID)
					})
				}
				if err != nil {
					log.Warn("preload finished with errors", "err", err)
				}
			}
			if !ready.Swap(true) {
				log.Info("snapshot ready", "role", f.role)
			}
		}
	}
}
