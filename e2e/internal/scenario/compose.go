package scenario

import (
	"context"

	"github.com/kudesn1k1/WasmHooks/e2e/internal/cpapi"
)

// stopService runs `docker compose stop <service>`; it returns once the
// container has exited.
func stopService(ctx context.Context, dir, service string) error {
	_, err := cpapi.Compose(ctx, dir, "stop", service)
	return err
}

// startService runs `docker compose start <service>`; it returns once the
// container is started, not once it is ready.
func startService(ctx context.Context, dir, service string) error {
	_, err := cpapi.Compose(ctx, dir, "start", service)
	return err
}
