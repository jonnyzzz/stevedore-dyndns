package mtproto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/9seconds/mtg/v2/essentials"
)

// ConnHandler is the minimal surface we depend on from mtglib.Proxy. Using an
// interface lets tests stub the MTProto layer without standing up a real
// proxy.
type ConnHandler interface {
	ServeConn(essentials.Conn)
}

// DispatcherConfig captures everything the dispatcher needs at startup.
type DispatcherConfig struct {
	// Listen is the "host:port" the dispatcher binds. Usually ":443".
	Listen string
	// Loopback is the "host:port" of the Caddy HTTPS listener that receives
	// forwarded non-MTProto traffic. Usually "127.0.0.1:8443".
	Loopback string
	// MaxConnections caps in-flight connections across all bound domains.
	// Additional connections are rejected immediately.
	MaxConnections int
	// HandshakeTimeout bounds the TLS ClientHello peek phase. A connection
	// that fails to produce a full TLS record within this window is closed.
	HandshakeTimeout time.Duration
	// Logger is used for structured logging; must be non-nil.
	Logger *slog.Logger
}

// Dispatcher is a TCP accept loop that routes each connection by SNI to
// either an MTProto proxy (per bound domain) or a loopback Caddy listener.
// It is safe to swap bindings at runtime via UpdateBindings.
type Dispatcher struct {
	cfg      DispatcherConfig
	bindings atomic.Pointer[map[string]ConnHandler]

	sem chan struct{}

	listener net.Listener
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewDispatcher validates the config and returns a ready-to-start dispatcher.
// Call Start to begin accepting connections.
func NewDispatcher(cfg DispatcherConfig) (*Dispatcher, error) {
	if cfg.Listen == "" {
		return nil, fmt.Errorf("mtproto dispatcher: Listen is required")
	}
	if cfg.Loopback == "" {
		return nil, fmt.Errorf("mtproto dispatcher: Loopback is required")
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 8192
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	d := &Dispatcher{
		cfg: cfg,
		sem: make(chan struct{}, cfg.MaxConnections),
	}
	empty := map[string]ConnHandler{}
	d.bindings.Store(&empty)
	return d, nil
}

// UpdateBindings replaces the SNI→handler map atomically. The previous map
// remains in use for connections that have already been dispatched. Callers
// that need to drain MTProto proxies for rotated secrets should perform the
// drain after UpdateBindings returns.
func (d *Dispatcher) UpdateBindings(m map[string]ConnHandler) {
	// Take a defensive copy so the caller can keep mutating their map.
	cp := make(map[string]ConnHandler, len(m))
	for k, v := range m {
		cp[k] = v
	}
	d.bindings.Store(&cp)
}

// Start begins accepting connections. It returns once the listener is ready
// or errors immediately on bind failure. Use Shutdown for orderly teardown.
func (d *Dispatcher) Start(parent context.Context) error {
	ln, err := net.Listen("tcp", d.cfg.Listen)
	if err != nil {
		return fmt.Errorf("mtproto dispatcher: listen %s: %w", d.cfg.Listen, err)
	}
	d.listener = ln
	d.ctx, d.cancel = context.WithCancel(parent)

	d.cfg.Logger.Info("MTProto dispatcher listening",
		"addr", ln.Addr().String(),
		"loopback", d.cfg.Loopback,
		"max_connections", d.cfg.MaxConnections,
	)

	d.wg.Add(1)
	go d.acceptLoop()
	return nil
}

// Shutdown stops accepting new connections and waits for the accept loop to
// return. Active connections are left to finish on their own.
func (d *Dispatcher) Shutdown(ctx context.Context) error {
	if d.listener != nil {
		_ = d.listener.Close()
	}
	if d.cancel != nil {
		d.cancel()
	}
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Addr returns the bound listen address (useful for tests that bind :0).
func (d *Dispatcher) Addr() net.Addr {
	if d.listener == nil {
		return nil
	}
	return d.listener.Addr()
}

func (d *Dispatcher) acceptLoop() {
	defer d.wg.Done()

	for {
		conn, err := d.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-d.ctx.Done():
				return
			default:
			}
			d.cfg.Logger.Warn("accept error", "error", err)
			continue
		}

		// Concurrency gate — refuse when the pool is saturated rather than
		// buffering up unbounded goroutines.
		select {
		case d.sem <- struct{}{}:
		default:
			d.cfg.Logger.Warn("mtproto dispatcher: capacity exceeded, dropping connection",
				"remote", conn.RemoteAddr().String())
			_ = conn.Close()
			continue
		}

		d.wg.Add(1)
		go func(c net.Conn) {
			defer d.wg.Done()
			defer func() { <-d.sem }()
			d.handle(c)
		}(conn)
	}
}

func (d *Dispatcher) handle(conn net.Conn) {
	log := d.cfg.Logger.With("remote", conn.RemoteAddr().String())

	if err := conn.SetReadDeadline(time.Now().Add(d.cfg.HandshakeTimeout)); err != nil {
		log.Warn("set read deadline", "error", err)
		_ = conn.Close()
		return
	}
	sni, raw, err := PeekSNI(conn)
	if err != nil {
		if errors.Is(err, ErrNotTLS) {
			log.Debug("non-TLS traffic on dispatcher port, closing")
		} else {
			log.Debug("ClientHello peek failed", "error", err)
		}
		_ = conn.Close()
		return
	}
	// Clear the deadline before handing off; downstream decides its own timeouts.
	_ = conn.SetReadDeadline(time.Time{})

	bindings := *d.bindings.Load()
	if handler, ok := bindings[sni]; ok {
		log.Debug("routing to MTProto proxy", "sni", sni)
		wrapped := essentials.WrapNetConn(newPrefixConn(conn, raw))
		handler.ServeConn(wrapped)
		return
	}

	// Forward to Caddy loopback.
	d.forwardLoopback(conn, raw, sni, log)
}

func (d *Dispatcher) forwardLoopback(conn net.Conn, prefix []byte, sni string, log *slog.Logger) {
	upstream, err := net.DialTimeout("tcp", d.cfg.Loopback, 5*time.Second)
	if err != nil {
		log.Warn("loopback dial failed", "target", d.cfg.Loopback, "error", err)
		_ = conn.Close()
		return
	}

	// Send the buffered ClientHello bytes first.
	if _, err := upstream.Write(prefix); err != nil {
		log.Warn("loopback prefix write", "error", err)
		_ = upstream.Close()
		_ = conn.Close()
		return
	}
	log.Debug("routing to Caddy loopback", "sni", sni)

	// Splice in both directions. Exit when either side closes.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		_ = upstream.(interface{ CloseWrite() error }).CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
	_ = upstream.Close()
	_ = conn.Close()
}

// prefixConn wraps a net.Conn, returning prefix bytes on the first Reads
// before falling through to the underlying conn. Writes and all other
// methods pass through. It satisfies essentials.Conn which embeds net.Conn.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func newPrefixConn(c net.Conn, prefix []byte) *prefixConn {
	// Copy so the handler cannot observe future mutations of the caller's
	// buffer.
	cp := make([]byte, len(prefix))
	copy(cp, prefix)
	return &prefixConn{Conn: c, prefix: cp}
}

func (p *prefixConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}
