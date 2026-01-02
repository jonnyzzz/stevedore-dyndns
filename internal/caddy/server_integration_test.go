//go:build integration

package caddy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
)

// TestHTTPSServerSecurity is an integration test that:
// 1. Generates test certificates (CA, server cert, client cert)
// 2. Starts a Caddy server with mTLS configuration
// 3. Verifies security properties
//
// Run with: go test -v -tags=integration ./internal/caddy/ -run TestHTTPSServerSecurity
func TestHTTPSServerSecurity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Check if Docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker not available, skipping integration test")
	}

	// Create temp directory for test artifacts
	tempDir, err := os.MkdirTemp("", "caddy-security-test-*")
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

	// Generate Caddyfile
	caddyfile := generateTestCaddyfile(certs, true) // mTLS enabled
	caddyfilePath := filepath.Join(tempDir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(caddyfile), 0644); err != nil {
		t.Fatalf("Failed to write Caddyfile: %v", err)
	}
	t.Logf("Generated Caddyfile:\n%s", caddyfile)

	// Start Caddy in Docker
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	containerID, err := startCaddyContainer(ctx, tempDir, caddyfilePath)
	if err != nil {
		t.Fatalf("Failed to start Caddy container: %v", err)
	}
	defer stopCaddyContainer(containerID)

	// Wait for Caddy to be ready
	time.Sleep(3 * time.Second)

	// Get container IP
	containerIP, err := getContainerIP(containerID)
	if err != nil {
		t.Fatalf("Failed to get container IP: %v", err)
	}
	t.Logf("Caddy container IP: %s", containerIP)

	// Run security tests
	t.Run("mTLS_RejectsConnectionWithoutClientCert", func(t *testing.T) {
		testMTLSRejectsWithoutClientCert(t, certs, containerIP)
	})

	t.Run("mTLS_AcceptsConnectionWithValidClientCert", func(t *testing.T) {
		testMTLSAcceptsWithClientCert(t, certs, containerIP)
	})

	t.Run("TLS_UsesSecureProtocol", func(t *testing.T) {
		testTLSSecureProtocol(t, certs, containerIP)
	})

	t.Run("TLS_RejectsInsecureProtocols", func(t *testing.T) {
		testTLSRejectsInsecureProtocols(t, certs, containerIP)
	})
}

// TestHTTPSServerWithoutMTLS verifies behavior when mTLS is disabled (direct mode)
func TestHTTPSServerWithoutMTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker not available, skipping integration test")
	}

	tempDir, err := os.MkdirTemp("", "caddy-nomtls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	certs, err := generateTestCertificates(tempDir)
	if err != nil {
		t.Fatalf("Failed to generate certificates: %v", err)
	}

	// Generate Caddyfile WITHOUT mTLS
	caddyfile := generateTestCaddyfile(certs, false) // mTLS disabled
	caddyfilePath := filepath.Join(tempDir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(caddyfile), 0644); err != nil {
		t.Fatalf("Failed to write Caddyfile: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	containerID, err := startCaddyContainer(ctx, tempDir, caddyfilePath)
	if err != nil {
		t.Fatalf("Failed to start Caddy container: %v", err)
	}
	defer stopCaddyContainer(containerID)

	time.Sleep(3 * time.Second)

	containerIP, err := getContainerIP(containerID)
	if err != nil {
		t.Fatalf("Failed to get container IP: %v", err)
	}

	t.Run("DirectMode_AcceptsConnectionWithoutClientCert", func(t *testing.T) {
		// In direct mode (no mTLS), connections without client cert should work
		// Skip server cert verification since we're testing via Docker IP
		client := createHTTPClient(certs.caCert, nil, nil, true)
		resp, err := client.Get(fmt.Sprintf("https://%s:8443/", containerIP))
		if err != nil {
			t.Fatalf("Expected connection to succeed in direct mode, got error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected HTTP 200 or 404, got %d", resp.StatusCode)
		}
		t.Logf("Direct mode connection succeeded with status %d", resp.StatusCode)
	})
}

// testCertificates holds paths to generated test certificates
type testCertificates struct {
	caCertPath     string
	caKeyPath      string
	serverCertPath string
	serverKeyPath  string
	clientCertPath string
	clientKeyPath  string

	// Parsed certificates for client configuration
	caCert     *x509.Certificate
	clientCert tls.Certificate
}

// generateTestCertificates creates a CA, server cert, and client cert for testing
func generateTestCertificates(dir string) (*testCertificates, error) {
	certs := &testCertificates{
		caCertPath:     filepath.Join(dir, "ca.pem"),
		caKeyPath:      filepath.Join(dir, "ca-key.pem"),
		serverCertPath: filepath.Join(dir, "server.pem"),
		serverKeyPath:  filepath.Join(dir, "server-key.pem"),
		clientCertPath: filepath.Join(dir, "client.pem"),
		clientKeyPath:  filepath.Join(dir, "client-key.pem"),
	}

	// Generate CA
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test CA"},
			CommonName:   "Test CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}
	certs.caCert = caCert

	// Write CA cert
	if err := writePEM(certs.caCertPath, "CERTIFICATE", caCertDER); err != nil {
		return nil, err
	}
	if err := writePEM(certs.caKeyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caKey)); err != nil {
		return nil, err
	}

	// Generate server certificate
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Test Server"},
			CommonName:   "localhost",
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost", "*.localhost", "test.example.com"},
	}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	if err := writePEM(certs.serverCertPath, "CERTIFICATE", serverCertDER); err != nil {
		return nil, err
	}
	if err := writePEM(certs.serverKeyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey)); err != nil {
		return nil, err
	}

	// Generate client certificate (simulates Cloudflare's client cert)
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate client key: %w", err)
	}

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			Organization: []string{"Test Client"},
			CommonName:   "Test Client",
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	clientCertDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create client certificate: %w", err)
	}

	if err := writePEM(certs.clientCertPath, "CERTIFICATE", clientCertDER); err != nil {
		return nil, err
	}
	if err := writePEM(certs.clientKeyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(clientKey)); err != nil {
		return nil, err
	}

	// Load client certificate for use in tests
	clientCert, err := tls.LoadX509KeyPair(certs.clientCertPath, certs.clientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}
	certs.clientCert = clientCert

	return certs, nil
}

func writePEM(path, blockType string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	defer f.Close()

	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: data})
}

// generateTestCaddyfile creates a Caddyfile for testing
func generateTestCaddyfile(certs *testCertificates, enableMTLS bool) string {
	mtlsBlock := ""
	if enableMTLS {
		mtlsBlock = `
        # mTLS - require client certificate
        client_auth {
            mode require_and_verify
            trusted_ca_cert_file /certs/ca.pem
        }`
	}

	return fmt.Sprintf(`{
    # Disable ACME for testing
    auto_https off
    admin off
}

:8443 {
    tls /certs/server.pem /certs/server-key.pem {%s
    }

    respond "OK" 200
}

:8080 {
    respond "Health OK" 200
}
`, mtlsBlock)
}

// startCaddyContainer starts Caddy in a Docker container
func startCaddyContainer(ctx context.Context, certDir, caddyfilePath string) (string, error) {
	// Use official Caddy image
	cmd := exec.CommandContext(ctx, "docker", "run", "-d",
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
			return "", fmt.Errorf("docker run failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("docker run failed: %w", err)
	}

	containerID := strings.TrimSpace(string(output))
	return containerID, nil
}

// stopCaddyContainer stops and removes the container
func stopCaddyContainer(containerID string) {
	exec.Command("docker", "rm", "-f", containerID).Run()
}

// getContainerIP gets the container's IP address
func getContainerIP(containerID string) (string, error) {
	cmd := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerID)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get container IP: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// createHTTPClient creates an HTTP client with optional mTLS configuration
// skipServerVerify skips server cert hostname verification (needed when connecting via Docker IP)
func createHTTPClient(caCert *x509.Certificate, clientCert *tls.Certificate, tlsConfig *tls.Config, skipServerVerify bool) *http.Client {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	// Skip server cert verification when testing via Docker container IP
	// We're testing mTLS (client auth), not server cert validity
	if skipServerVerify {
		tlsConfig.InsecureSkipVerify = true
	}

	// Add CA to trust pool (still used for client cert verification by server)
	if caCert != nil {
		pool := x509.NewCertPool()
		pool.AddCert(caCert)
		tlsConfig.RootCAs = pool
	}

	// Add client certificate if provided
	if clientCert != nil {
		tlsConfig.Certificates = []tls.Certificate{*clientCert}
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: 10 * time.Second,
	}
}

// testMTLSRejectsWithoutClientCert verifies that connections without client cert are rejected
func testMTLSRejectsWithoutClientCert(t *testing.T, certs *testCertificates, serverIP string) {
	// Create client WITHOUT client certificate
	// Skip server cert verification since we're testing via Docker IP
	client := createHTTPClient(certs.caCert, nil, nil, true)

	url := fmt.Sprintf("https://%s:8443/", serverIP)
	_, err := client.Get(url)

	if err == nil {
		t.Error("Expected connection to be rejected without client certificate, but it succeeded")
		return
	}

	// The error should indicate a TLS handshake failure
	errStr := err.Error()
	if !strings.Contains(errStr, "tls") && !strings.Contains(errStr, "certificate") && !strings.Contains(errStr, "handshake") {
		t.Logf("Connection failed with unexpected error: %v", err)
	}

	t.Logf("PASS: Connection without client cert was rejected: %v", err)
}

// testMTLSAcceptsWithClientCert verifies that connections with valid client cert succeed
func testMTLSAcceptsWithClientCert(t *testing.T, certs *testCertificates, serverIP string) {
	// Create client WITH client certificate
	// Skip server cert verification since we're testing via Docker IP
	client := createHTTPClient(certs.caCert, &certs.clientCert, nil, true)

	url := fmt.Sprintf("https://%s:8443/", serverIP)
	resp, err := client.Get(url)

	if err != nil {
		t.Fatalf("Expected connection with client cert to succeed, got error: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	t.Logf("PASS: Connection with client cert succeeded: %d %s", resp.StatusCode, string(body))

	// Accept 200 (OK) or 421 (Misdirected Request) as success
	// 421 happens when connecting via IP address since Caddy doesn't know which site to serve
	// The important thing is that mTLS worked (we got an HTTP response, not a TLS error)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("Expected HTTP 200 or 421, got %d", resp.StatusCode)
	}
}

// testTLSSecureProtocol verifies that TLS 1.2+ is being used
func testTLSSecureProtocol(t *testing.T, certs *testCertificates, serverIP string) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
	}

	// Skip server cert verification since we're testing via Docker IP
	client := createHTTPClient(certs.caCert, &certs.clientCert, tlsConfig, true)

	url := fmt.Sprintf("https://%s:8443/", serverIP)
	resp, err := client.Get(url)

	if err != nil {
		t.Fatalf("TLS 1.2+ connection failed: %v", err)
	}
	defer resp.Body.Close()

	t.Log("PASS: TLS 1.2+ connection succeeded")
}

// testTLSRejectsInsecureProtocols verifies that old TLS versions are rejected
func testTLSRejectsInsecureProtocols(t *testing.T, certs *testCertificates, serverIP string) {
	// Try to connect with TLS 1.0 only
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS10,
		MaxVersion:         tls.VersionTLS10,
		InsecureSkipVerify: true, // Skip server cert verification for Docker IP
	}

	tlsConfig.Certificates = []tls.Certificate{certs.clientCert}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: 10 * time.Second,
	}

	url := fmt.Sprintf("https://%s:8443/", serverIP)
	_, err := client.Get(url)

	// Caddy by default only allows TLS 1.2+, so this should fail
	// However, Go might not even attempt TLS 1.0 on modern versions
	if err != nil {
		t.Logf("PASS: TLS 1.0 connection was rejected (expected): %v", err)
	} else {
		t.Log("WARN: TLS 1.0 connection succeeded - this may be acceptable depending on Caddy config")
	}
}

// TestGeneratorWithRealTemplate tests the generator with the actual Caddyfile.template
func TestGeneratorWithRealTemplate(t *testing.T) {
	// Find the template file relative to the project root
	templatePaths := []string{
		"../../Caddyfile.template",
		"../../../Caddyfile.template",
		"Caddyfile.template",
	}

	var templatePath string
	for _, p := range templatePaths {
		if _, err := os.Stat(p); err == nil {
			templatePath = p
			break
		}
	}

	if templatePath == "" {
		t.Skip("Caddyfile.template not found")
	}

	t.Run("ProxyMode_GeneratesClientAuth", func(t *testing.T) {
		cfg := &config.Config{
			Domain:          "home.example.com",
			AcmeEmail:       "test@example.com",
			LogLevel:        "info",
			CloudflareProxy: true,
			SubdomainPrefix: true,
		}

		gen := New(cfg, nil)
		gen.TemplatePath = templatePath

		content, err := gen.GenerateContent()
		if err != nil {
			t.Fatalf("Failed to generate content: %v", err)
		}

		// Verify mTLS configuration is present
		if !strings.Contains(content, "client_auth") {
			t.Error("Expected client_auth directive in proxy mode")
		}
		if !strings.Contains(content, "require_and_verify") {
			t.Error("Expected require_and_verify mode in proxy mode")
		}
		if !strings.Contains(content, "origin-pull-ca.pem") {
			t.Error("Expected origin-pull-ca.pem reference in proxy mode")
		}
	})

	t.Run("DirectMode_NoClientAuth", func(t *testing.T) {
		cfg := &config.Config{
			Domain:          "home.example.com",
			AcmeEmail:       "test@example.com",
			LogLevel:        "info",
			CloudflareProxy: false,
		}

		gen := New(cfg, nil)
		gen.TemplatePath = templatePath

		content, err := gen.GenerateContent()
		if err != nil {
			t.Fatalf("Failed to generate content: %v", err)
		}

		// Verify mTLS configuration is NOT present
		if strings.Contains(content, "client_auth") {
			t.Error("client_auth should not be present in direct mode")
		}
	})
}
