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

	if err := waitForServer(ctx, "127.0.0.1", httpPort); err != nil {
		t.Fatalf("server not ready: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%s/", httpPort), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "example.test"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
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
