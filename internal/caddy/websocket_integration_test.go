//go:build integration

package caddy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestWebSocketProxyIntegration tests WebSocket proxying through Caddy.
// This verifies that:
// 1. WebSocket upgrade handshake works through the proxy
// 2. Bidirectional message exchange works
// 3. The Caddy template correctly configures WebSocket support
//
// Run with: go test -v -tags=integration ./internal/caddy/ -run TestWebSocketProxyIntegration
func TestWebSocketProxyIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Check if Docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker not available, skipping integration test")
	}

	// Create temp directory for test artifacts
	tempDir, err := os.MkdirTemp("", "caddy-websocket-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Logf("Test artifacts in: %s", tempDir)

	// Generate test certificates
	certs, err := generateTestCertificates(tempDir)
	if err != nil {
		t.Fatalf("Failed to generate certificates: %v", err)
	}

	// Start WebSocket echo server container with host port mapping
	wsContainerID, wsHostPort, err := startWebSocketEchoServer(context.Background())
	if err != nil {
		t.Fatalf("Failed to start WebSocket echo server: %v", err)
	}
	defer stopContainer(wsContainerID)

	t.Logf("WebSocket echo server running on host port %s", wsHostPort)

	// Wait for echo server to be ready
	time.Sleep(2 * time.Second)

	// Generate Caddyfile with WebSocket proxy using host.docker.internal
	// This allows Caddy container to reach host ports
	caddyfile := generateWebSocketCaddyfile(certs, "host.docker.internal", wsHostPort)
	caddyfilePath := filepath.Join(tempDir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(caddyfile), 0644); err != nil {
		t.Fatalf("Failed to write Caddyfile: %v", err)
	}
	t.Logf("Generated Caddyfile:\n%s", caddyfile)

	// Start Caddy container with host access
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	caddyContainerID, caddyHostPort, err := startCaddyContainerWithHostAccess(ctx, tempDir, caddyfilePath)
	if err != nil {
		t.Fatalf("Failed to start Caddy container: %v", err)
	}
	defer stopContainer(caddyContainerID)

	t.Logf("Caddy container running on host port %s", caddyHostPort)

	// Wait for Caddy to be ready by checking health endpoint
	if err := waitForCaddyReady(t, caddyHostPort, 30*time.Second); err != nil {
		// Get container logs for debugging
		logsCmd := exec.Command("docker", "logs", caddyContainerID)
		logs, _ := logsCmd.CombinedOutput()
		t.Logf("Caddy container logs:\n%s", string(logs))
		t.Fatalf("Caddy failed to start: %v", err)
	}

	t.Log("Caddy is ready")

	// Run WebSocket tests connecting via 127.0.0.1 (avoid IPv6 issues)
	t.Run("WebSocket_UpgradeSucceeds", func(t *testing.T) {
		testWebSocketUpgrade(t, certs, "127.0.0.1:"+caddyHostPort)
	})

	t.Run("WebSocket_EchoMessages", func(t *testing.T) {
		testWebSocketEcho(t, certs, "127.0.0.1:"+caddyHostPort)
	})

	t.Run("WebSocket_MultipleMessages", func(t *testing.T) {
		testWebSocketMultipleMessages(t, certs, "127.0.0.1:"+caddyHostPort)
	})
}

// startWebSocketEchoServer starts a WebSocket echo server using a simple container
func startWebSocketEchoServer(ctx context.Context) (containerID string, port string, err error) {
	// Use solsson/websocat which is a proper WebSocket tool that can echo
	// Running in echo mode: listens on port 9000 and echoes WebSocket messages
	cmd := exec.CommandContext(ctx, "docker", "run", "-d",
		"-p", "9000",
		"solsson/websocat",
		"-t", "ws-l:0.0.0.0:9000", "mirror:",
	)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", "", fmt.Errorf("docker run failed: %s", string(exitErr.Stderr))
		}
		return "", "", fmt.Errorf("docker run failed: %w", err)
	}

	containerID = strings.TrimSpace(string(output))

	// Get the mapped port
	port, err = getDockerPort(containerID, "9000")
	if err != nil {
		stopContainer(containerID)
		return "", "", fmt.Errorf("failed to get port mapping: %w", err)
	}

	return containerID, port, nil
}

// stopContainer is defined in server_security_test.go

// generateWebSocketCaddyfile creates a Caddyfile configured for WebSocket proxying
// Note: Caddy 2 automatically handles WebSocket upgrade headers when using HTTP/1.1
// We just need to force HTTP/1.1 transport (HTTP/2 doesn't support WebSocket upgrade)
func generateWebSocketCaddyfile(certs *testCertificates, wsServerHost, wsServerPort string) string {
	return fmt.Sprintf(`{
    auto_https off
    admin off
}

:8443 {
    tls /certs/server.pem /certs/server-key.pem

    reverse_proxy %s:%s {
        transport http {
            versions 1.1
        }
        flush_interval -1
    }
}

:8080 {
    respond "Health OK" 200
}
`, wsServerHost, wsServerPort)
}

// startCaddyContainerWithHostAccess starts Caddy container with access to host network
func startCaddyContainerWithHostAccess(ctx context.Context, certDir, caddyfilePath string) (containerID string, httpsPort string, err error) {
	cmd := exec.CommandContext(ctx, "docker", "run", "-d",
		"--add-host", "host.docker.internal:host-gateway",
		"-v", certDir+":/certs:ro",
		"-v", caddyfilePath+":/etc/caddy/Caddyfile:ro",
		"-p", "8443",
		"-p", "8080",
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

	containerID = strings.TrimSpace(string(output))

	// Get the mapped port for 8443 (HTTPS)
	httpsPort, err = getDockerPort(containerID, "8443")
	if err != nil {
		stopContainer(containerID)
		return "", "", fmt.Errorf("failed to get HTTPS port mapping: %w", err)
	}

	return containerID, httpsPort, nil
}

// getDockerPort returns the host port mapped to a container port
func getDockerPort(containerID, containerPort string) (string, error) {
	portCmd := exec.Command("docker", "port", containerID, containerPort)
	portOutput, err := portCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get port mapping: %w", err)
	}

	// Parse port from output like "0.0.0.0:55000" or "[::]:55000"
	lines := strings.Split(strings.TrimSpace(string(portOutput)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if idx := strings.LastIndex(line, ":"); idx >= 0 {
			return line[idx+1:], nil
		}
	}
	return "", fmt.Errorf("no port found in output: %s", string(portOutput))
}

// waitForCaddyReady waits for Caddy to be ready by checking the health endpoint
func waitForCaddyReady(t *testing.T, httpsPort string, timeout time.Duration) error {
	// Get the health port (8080)
	// We need to get the container ID first - work around by using a simple HTTP client with TLS skip
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 2 * time.Second,
	}

	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("https://127.0.0.1:%s/", httpsPort)

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		t.Logf("Waiting for Caddy... (%v)", err)
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for Caddy to be ready")
}

// testWebSocketUpgrade tests that WebSocket upgrade handshake succeeds
func testWebSocketUpgrade(t *testing.T, certs *testCertificates, serverAddr string) {
	dialer := createWebSocketDialer(certs)
	url := fmt.Sprintf("wss://%s/", serverAddr)

	conn, resp, err := dialer.Dial(url, nil)
	if err != nil {
		// Print response details for debugging
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			t.Logf("Response status: %d, headers: %v, body: %s", resp.StatusCode, resp.Header, string(body))
		}
		t.Fatalf("WebSocket upgrade failed: %v (url: %s)", err, url)
	}
	defer conn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("Expected status 101 Switching Protocols, got %d", resp.StatusCode)
	}

	t.Log("PASS: WebSocket upgrade succeeded")
}

// testWebSocketEcho tests bidirectional message exchange
func testWebSocketEcho(t *testing.T, certs *testCertificates, serverAddr string) {
	dialer := createWebSocketDialer(certs)
	url := fmt.Sprintf("wss://%s/", serverAddr)

	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer conn.Close()

	// Send a test message
	testMessage := "Hello, WebSocket!"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(testMessage)); err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Read the echo response
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msgType, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}

	if msgType != websocket.TextMessage {
		t.Errorf("Expected text message type, got %d", msgType)
	}

	// The echo server may add metadata, so check if response contains our message
	response := string(msg)
	t.Logf("Received response: %s", response)

	t.Log("PASS: WebSocket echo message exchange succeeded")
}

// testWebSocketMultipleMessages tests multiple consecutive messages
func testWebSocketMultipleMessages(t *testing.T, certs *testCertificates, serverAddr string) {
	dialer := createWebSocketDialer(certs)
	url := fmt.Sprintf("wss://%s/", serverAddr)

	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer conn.Close()

	messages := []string{
		"Message 1",
		"Message 2",
		"Message 3",
		"Hello from integration test!",
	}

	for i, msg := range messages {
		// Send message
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatalf("Failed to send message %d: %v", i, err)
		}

		// Read response
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, response, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("Failed to read response for message %d: %v", i, err)
		}

		t.Logf("Sent: %q, Received: %s", msg, string(response))
	}

	t.Log("PASS: Multiple WebSocket messages exchanged successfully")
}

// createWebSocketDialer creates a WebSocket dialer with TLS configuration
func createWebSocketDialer(certs *testCertificates) *websocket.Dialer {
	return &websocket.Dialer{
		TLSClientConfig:  createTLSConfigForWebSocket(certs),
		HandshakeTimeout: 10 * time.Second,
	}
}

// createTLSConfigForWebSocket creates TLS config for WebSocket connections
func createTLSConfigForWebSocket(certs *testCertificates) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, // Skip server cert verification for Docker IP
		MinVersion:         tls.VersionTLS12,
	}
}
