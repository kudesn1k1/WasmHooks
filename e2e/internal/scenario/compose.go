package scenario

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// compose runs `docker compose <args>` in dir, the directory with
// compose.yaml (the project name comes from the file), and returns stdout. A
// non-zero exit becomes an error carrying the tail of stderr; stdout and
// stderr are kept apart because the control plane CLI prints its result
// alone on stdout and chatter on stderr.
func compose(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("docker compose %s: %w: %s",
			strings.Join(args, " "), err, lastLine(stderr.String()))
	}
	return stdout.String(), nil
}

// controlPlaneCLI runs `controlplane <args>` inside the control-plane
// container: `docker compose exec -T control-plane controlplane ...`.
func controlPlaneCLI(ctx context.Context, dir string, args ...string) (string, error) {
	return compose(ctx, dir, append([]string{"exec", "-T", "control-plane", "controlplane"}, args...)...)
}

// stopService runs `docker compose stop <service>`; it returns once the
// container has exited.
func stopService(ctx context.Context, dir, service string) error {
	_, err := compose(ctx, dir, "stop", service)
	return err
}

// startService runs `docker compose start <service>`; it returns once the
// container is started, not once it is ready.
func startService(ctx context.Context, dir, service string) error {
	_, err := compose(ctx, dir, "start", service)
	return err
}

// lastLine is the last non-empty line of s: for a Python traceback or a
// docker error it is the line that says what went wrong.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
