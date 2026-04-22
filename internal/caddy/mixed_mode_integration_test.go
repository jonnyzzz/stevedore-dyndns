//go:build integration

package caddy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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
)

// TestMixedMode451Integration stands up Caddy with self-signed certs and a
// Caddyfile shaped like the generator's output (proxy block + direct site +
// 451 catchall + default_sni). It verifies the three host-validation paths
// that matter in production:
//
//  1. Known proxy Host + valid client cert → the proxied site responds.
//  2. Known direct Host + no client cert → the direct site responds.
//  3. Known SNI + unknown Host within the zone → 451 from the proxy-block
//     fallback.
//  4. SNI + Host = catchall → 451 from the dedicated catchall site.
func TestMixedMode451Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	tmpDir := tempDir(t, "caddy-mixed-mode-")

	// *.test covers the "unknown host within zone" scenario where the server
	// has to serve a cert for a subdomain that is not explicitly configured.
	hosts := []string{
		"app1.test",
		"direct1.test",
		"catchall.test",
		"notreal.test",
		"*.test",
	}
	certs, err := generateMixedModeTestCerts(tmpDir, hosts)
	if err != nil {
		t.Fatalf("generate certs: %v", err)
	}

	caddyfile := renderMixedModeCaddyfile()
	caddyfilePath := filepath.Join(tmpDir, "Caddyfile")
	if err := os.WriteFile(caddyfilePath, []byte(caddyfile), 0644); err != nil {
		t.Fatalf("write caddyfile: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	containerID, httpsPort, err := startCaddyContainer(ctx, tmpDir, caddyfilePath)
	if err != nil {
		t.Fatalf("start caddy: %v", err)
	}

	// t.Cleanup runs LIFO, so register log-dump FIRST, stop-container SECOND —
	// that way logs are captured before the container is removed.
	t.Cleanup(func() { stopContainer(containerID) })
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := getContainerLogs(containerID)
			t.Logf("Caddy container logs:\n%s", logs)
		}
	})

	if err := waitForServer(ctx, "127.0.0.1", httpsPort); err != nil {
		logs, _ := getContainerLogs(containerID)
		t.Fatalf("server not ready: %v\ncontainer logs:\n%s", err, logs)
	}

	// Give Caddy a short grace period to finish listener setup.
	time.Sleep(500 * time.Millisecond)

	serverAddr := net.JoinHostPort("127.0.0.1", httpsPort)

	clientCertKeypair, err := tls.LoadX509KeyPair(certs.clientCertPath, certs.clientKeyPath)
	if err != nil {
		t.Fatalf("load client cert: %v", err)
	}

	t.Run("proxy_site_with_client_cert_returns_200", func(t *testing.T) {
		resp, body := httpsGet(t, serverAddr, "app1.test", &clientCertKeypair)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%q", resp.StatusCode, body)
		}
		if !strings.Contains(body, "proxy-ok") {
			t.Fatalf("expected proxy-ok body, got %q", body)
		}
	})

	t.Run("direct_site_without_client_cert_returns_200", func(t *testing.T) {
		resp, body := httpsGet(t, serverAddr, "direct1.test", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%q", resp.StatusCode, body)
		}
		if !strings.Contains(body, "direct-ok") {
			t.Fatalf("expected direct-ok body, got %q", body)
		}
	})

	t.Run("unknown_host_within_zone_returns_451", func(t *testing.T) {
		resp, body := httpsGet(t, serverAddr, "notreal.test", &clientCertKeypair)
		if resp.StatusCode != 451 {
			t.Fatalf("status=%d body=%q (expected 451)", resp.StatusCode, body)
		}
	})

	t.Run("catchall_site_returns_451", func(t *testing.T) {
		resp, body := httpsGet(t, serverAddr, "catchall.test", nil)
		if resp.StatusCode != 451 {
			t.Fatalf("status=%d body=%q (expected 451)", resp.StatusCode, body)
		}
	})
}

// renderMixedModeCaddyfile produces a Caddyfile modelled after the generator's
// output but with self-signed cert files in place of the ACME/DNS-01 block so
// the test does not hit Cloudflare or Let's Encrypt.
func renderMixedModeCaddyfile() string {
	return `{
    auto_https off
    admin off
    https_port 8443
    default_sni catchall.test
}

# Proxy-mode wildcard block — mTLS required; known subdomains handled,
# everything else falls through to 451.
*.test, test {
    tls /certs/server.pem /certs/server-key.pem {
        client_auth {
            mode require_and_verify
            trusted_ca_cert_file /certs/ca.pem
        }
    }

    @app1 host app1.test
    handle @app1 {
        respond "proxy-ok" 200
    }

    handle {
        respond "451 Unavailable For Legal Reasons" 451
    }
}

# Direct-mode site — same cert, no client_auth.
direct1.test {
    tls /certs/server.pem /certs/server-key.pem

    respond "direct-ok" 200
}

# 451 catchall — own cert via default_sni; always answers 451.
catchall.test {
    tls /certs/server.pem /certs/server-key.pem

    respond "451 Unavailable For Legal Reasons" 451
}
`
}

// httpsGet issues an HTTPS request with the given SNI + Host and an optional
// client certificate. Server certificate verification is skipped because the
// certs are self-signed and the test does not need to validate the chain.
func httpsGet(t *testing.T, serverAddr, host string, clientCert *tls.Certificate) (*http.Response, string) {
	t.Helper()

	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         host,
	}
	if clientCert != nil {
		tlsCfg.Certificates = []tls.Certificate{*clientCert}
	}

	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
		DialTLSContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return tls.Dial(network, serverAddr, tlsCfg)
		},
	}

	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://%s/", host), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

// generateMixedModeTestCerts creates a CA, server cert covering the given
// hostnames, and a client cert signed by the same CA.
func generateMixedModeTestCerts(dir string, hostnames []string) (*testCertificates, error) {
	certs := &testCertificates{
		caCertPath:     filepath.Join(dir, "ca.pem"),
		caKeyPath:      filepath.Join(dir, "ca-key.pem"),
		serverCertPath: filepath.Join(dir, "server.pem"),
		serverKeyPath:  filepath.Join(dir, "server-key.pem"),
		clientCertPath: filepath.Join(dir, "client.pem"),
		clientKeyPath:  filepath.Join(dir, "client-key.pem"),
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mixed-mode test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	certs.caCert = caCert

	if err := writePEMFile(certs.caCertPath, "CERTIFICATE", caDER); err != nil {
		return nil, err
	}
	if err := writePEMFile(certs.caKeyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caKey)); err != nil {
		return nil, err
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serverTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: hostnames[0]},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     hostnames,
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	if err := writePEMFile(certs.serverCertPath, "CERTIFICATE", serverDER); err != nil {
		return nil, err
	}
	if err := writePEMFile(certs.serverKeyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey)); err != nil {
		return nil, err
	}

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "mixed-mode test client"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	if err := writePEMFile(certs.clientCertPath, "CERTIFICATE", clientDER); err != nil {
		return nil, err
	}
	if err := writePEMFile(certs.clientKeyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(clientKey)); err != nil {
		return nil, err
	}
	return certs, nil
}
