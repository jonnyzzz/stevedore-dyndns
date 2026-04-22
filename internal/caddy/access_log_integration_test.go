//go:build integration

package caddy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccessLogFileIncludesHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tmpDir := tempDir(t, "caddy-access-log-")

	caddyfile := `{
    auto_https off
    admin off
}

:8080 {
    log {
        output stdout
        format json
    }

    respond "OK" 200
}
`

	caddyfilePath := filepath.Join(tmpDir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(caddyfile), 0644); err != nil {
		t.Fatalf("failed to write Caddyfile: %v", err)
	}

	containerID, httpPort, err := startCaddyContainerForAccessLog(ctx, caddyfilePath)
	if err != nil {
		t.Fatalf("failed to start Caddy container: %v", err)
	}
	defer stopContainer(containerID)

	// A TCP dial is satisfied by docker-proxy on Linux before Caddy has bound
	// its listener inside the container, so we retry the HTTP request until
	// the upstream is actually serving (or the context expires).
	url := fmt.Sprintf("http://127.0.0.1:%s/", httpPort)
	resp, err := getWithRetry(ctx, url, "example.test", 30*time.Second)
	if err != nil {
		t.Fatalf("failed to reach Caddy: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected response status: %d", resp.StatusCode)
	}

	needle := `"host":"example.test"`
	if err := waitForAccessLogEntry(t, containerID, needle, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

// getWithRetry sends GET requests with a Host header until one succeeds with
// an HTTP response, or the deadline expires.
func getWithRetry(ctx context.Context, url, host string, timeout time.Duration) (*http.Response, error) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 5 * time.Second}

	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Host = host

		resp, err := client.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("no HTTP response within %s: %w", timeout, lastErr)
}

func startCaddyContainerForAccessLog(ctx context.Context, caddyfilePath string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "docker", "run", "-d",
		"-v", caddyfilePath+":/etc/caddy/Caddyfile:ro",
		"-p", "0:8080",
		"caddy:2-alpine",
		"caddy", "run", "--config", "/etc/caddy/Caddyfile",
	)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", "", fmt.Errorf("docker run failed: %s", string(exitErr.Stderr))
		}
		return "", "", fmt.Errorf("docker run failed: %w", err)
	}

	containerID := strings.TrimSpace(string(output))
	httpPort, err := getDockerPort(containerID, "8080")
	if err != nil {
		stopContainer(containerID)
		return "", "", fmt.Errorf("failed to get HTTP port mapping: %w", err)
	}

	return containerID, httpPort, nil
}
