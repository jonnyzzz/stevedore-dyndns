package mtproto

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestPeekSNI_RealClientHello generates a real TLS ClientHello with
// crypto/tls and asserts the sniffer extracts the server_name.
func TestPeekSNI_RealClientHello(t *testing.T) {
	serverRead, clientWrite := net.Pipe()
	defer serverRead.Close()
	defer clientWrite.Close()

	// Launch a fake client that sends a ClientHello with SNI=target.example.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		tlsConn := tls.Client(clientWrite, &tls.Config{
			ServerName:         "target.example",
			InsecureSkipVerify: true,
		})
		// Handshake will fail (server side is a test reader that doesn't
		// respond), but the client still writes the ClientHello first.
		_ = tlsConn.HandshakeContext(ctx)
	}()

	_ = serverRead.SetReadDeadline(time.Now().Add(3 * time.Second))
	host, raw, err := PeekSNI(serverRead)
	if err != nil && !isAcceptablePostPeekErr(err) {
		t.Fatalf("PeekSNI: unexpected error %v", err)
	}
	if host != "target.example" {
		t.Errorf("host = %q, want target.example", host)
	}
	if len(raw) < 5 || raw[0] != 0x16 {
		t.Errorf("raw doesn't look like a TLS record header: % x", raw[:min(8, len(raw))])
	}
}

func TestPeekSNI_NotTLS(t *testing.T) {
	// HTTP/1.1 request
	reader := bytes.NewReader([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	_, _, err := PeekSNI(reader)
	if !errors.Is(err, ErrNotTLS) {
		t.Fatalf("expected ErrNotTLS, got %v", err)
	}
}

func TestPeekSNI_TruncatedHeader(t *testing.T) {
	_, _, err := PeekSNI(bytes.NewReader([]byte{0x16, 0x03}))
	if err == nil {
		t.Fatal("expected error on truncated header")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF-family, got %v", err)
	}
}

func TestPeekSNI_NoSNIExtension(t *testing.T) {
	// Minimum-length ClientHello with no extensions.
	body := []byte{}
	// legacy_version + random
	body = append(body, 0x03, 0x03)
	body = append(body, bytes.Repeat([]byte{0x00}, 32)...)
	// session_id (empty)
	body = append(body, 0x00)
	// cipher_suites — one suite (2 bytes payload)
	body = append(body, 0x00, 0x02, 0x00, 0x2f)
	// compression_methods — single null method
	body = append(body, 0x01, 0x00)
	// no extensions section

	// Wrap with handshake header (0x01 + 3-byte length) and TLS record (0x16 + version + length)
	handshake := append([]byte{0x01, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	record := append([]byte{0x16, 0x03, 0x03, byte(len(handshake) >> 8), byte(len(handshake))}, handshake...)

	host, _, err := PeekSNI(bytes.NewReader(record))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "" {
		t.Errorf("host = %q, want empty (no SNI)", host)
	}
}

func TestPeekSNI_RecordTooLarge(t *testing.T) {
	// Claims a 17 KiB record — above our cap.
	hdr := []byte{0x16, 0x03, 0x03, 0x44, 0x00}
	_, _, err := PeekSNI(bytes.NewReader(hdr))
	if !errors.Is(err, ErrNotTLS) {
		t.Fatalf("expected ErrNotTLS, got %v", err)
	}
}

// isAcceptablePostPeekErr allows the net.Pipe-based test to tolerate errors
// that happen AFTER the ClientHello is written (read deadline expires waiting
// for server response). PeekSNI itself must have succeeded first.
func isAcceptablePostPeekErr(err error) bool {
	if err == nil {
		return true
	}
	return strings.Contains(err.Error(), "deadline") ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe)
}

