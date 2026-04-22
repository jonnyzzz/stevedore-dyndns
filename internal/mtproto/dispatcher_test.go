package mtproto

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/9seconds/mtg/v2/essentials"
)

// stubHandler captures invocations of ServeConn for assertion.
type stubHandler struct {
	mu       sync.Mutex
	called   bool
	firstRaw []byte
}

func (s *stubHandler) ServeConn(c essentials.Conn) {
	buf := make([]byte, 8)
	n, _ := io.ReadFull(c, buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called = true
	s.firstRaw = append(s.firstRaw, buf[:n]...)
	_ = c.Close()
}

func newTestDispatcher(t *testing.T, loopbackAddr string) *Dispatcher {
	t.Helper()
	cfg := DispatcherConfig{
		Listen:           "127.0.0.1:0",
		Loopback:         loopbackAddr,
		MaxConnections:   8,
		HandshakeTimeout: 2 * time.Second,
		Logger:           slog.Default(),
	}
	d, err := NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})
	return d
}

// newLoopbackEcho stands up a TCP listener that echoes the first TLS record
// header back so we can assert dispatcher forwarded bytes correctly.
func newLoopbackEcho(t *testing.T) (addr string, received *bytes.Buffer, wait func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	received = &bytes.Buffer{}
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 4096)
		n, _ := c.Read(buf)
		mu.Lock()
		received.Write(buf[:n])
		mu.Unlock()
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})

	return ln.Addr().String(), received, func() { <-done }
}

func TestDispatcher_ForwardsNonBoundSNIToLoopback(t *testing.T) {
	loopback, received, wait := newLoopbackEcho(t)
	d := newTestDispatcher(t, loopback)

	client, err := net.Dial("tcp", d.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Client sends a ClientHello with SNI=unbound.example.
	done := make(chan struct{})
	go func() {
		defer close(done)
		tlsCfg := &tls.Config{ServerName: "unbound.example", InsecureSkipVerify: true}
		_ = tls.Client(client, tlsCfg).HandshakeContext(timeoutCtx(t, 2 * time.Second))
	}()
	<-done
	_ = client.Close()
	wait()

	if received.Len() < 5 {
		t.Fatalf("loopback received %d bytes, expected ≥5", received.Len())
	}
	if received.Bytes()[0] != 0x16 {
		t.Fatalf("loopback did not see TLS record: % x", received.Bytes()[:min(8, received.Len())])
	}
}

func TestDispatcher_RoutesBoundSNIToHandler(t *testing.T) {
	loopback, _, _ := newLoopbackEcho(t)
	d := newTestDispatcher(t, loopback)

	stub := &stubHandler{}
	d.UpdateBindings(map[string]ConnHandler{"bound.example": stub})

	client, err := net.Dial("tcp", d.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	go func() {
		tlsCfg := &tls.Config{ServerName: "bound.example", InsecureSkipVerify: true}
		_ = tls.Client(client, tlsCfg).HandshakeContext(timeoutCtx(t, 2 * time.Second))
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stub.mu.Lock()
		called := stub.called
		stub.mu.Unlock()
		if called {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if !stub.called {
		t.Fatal("handler was not invoked for bound SNI")
	}
	if len(stub.firstRaw) < 5 || stub.firstRaw[0] != 0x16 {
		t.Fatalf("handler did not receive ClientHello prefix: % x", stub.firstRaw)
	}
}

func TestDispatcher_ClosesNonTLS(t *testing.T) {
	loopback, received, _ := newLoopbackEcho(t)
	d := newTestDispatcher(t, loopback)

	client, err := net.Dial("tcp", d.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Send plaintext garbage.
	_, _ = client.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	_ = client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 16)
	_, _ = client.Read(buf)
	_ = client.Close()

	// Loopback should NOT have received anything.
	time.Sleep(100 * time.Millisecond)
	if received.Len() != 0 {
		t.Fatalf("loopback unexpectedly received %d bytes for non-TLS input", received.Len())
	}
}

func TestPrefixConn_ReadReplaysPrefixBeforeUnderlying(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() { _, _ = b.Write([]byte("REAL")) }()

	pc := newPrefixConn(a, []byte("FAKE"))
	buf := make([]byte, 4)
	_, err := io.ReadFull(pc, buf)
	if err != nil {
		t.Fatalf("read prefix: %v", err)
	}
	if string(buf) != "FAKE" {
		t.Errorf("prefix replay got %q, want FAKE", buf)
	}
	_, err = io.ReadFull(pc, buf)
	if err != nil {
		t.Fatalf("read underlying: %v", err)
	}
	if string(buf) != "REAL" {
		t.Errorf("underlying read got %q, want REAL", buf)
	}
}

func timeoutCtx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
