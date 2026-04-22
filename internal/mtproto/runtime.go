package mtproto

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/9seconds/mtg/v2/antireplay"
	"github.com/9seconds/mtg/v2/events"
	"github.com/9seconds/mtg/v2/ipblocklist"
	"github.com/9seconds/mtg/v2/mtglib"
	network "github.com/9seconds/mtg/v2/network/v2"
)

// RuntimeConfig bundles everything the runtime needs from dyndns.
type RuntimeConfig struct {
	// Bindings is the subdomain+fqdn list to bind MTProto to. Secrets are
	// looked up or generated under Store.
	Bindings []SubdomainBinding
	// Store persists the generated secrets.
	Store *Store
	// Dispatcher config (Listen, Loopback, etc.). Logger will be set from
	// Logger below if unset.
	Dispatcher DispatcherConfig
	// Logger is the structured logger for MTProto subsystems.
	Logger *slog.Logger
	// AntiReplayCacheSize sizes the shared stable bloom filter (bytes).
	// 0 → use mtg's default.
	AntiReplayCacheSize uint
}

// SubdomainBinding identifies a subdomain that should be bound to MTProto.
type SubdomainBinding struct {
	Subdomain string
	FQDN      string
}

// Runtime owns the dispatcher and the per-subdomain mtglib.Proxy instances.
// It is built from RuntimeConfig at startup and shut down in reverse order
// via Shutdown.
type Runtime struct {
	cfg        RuntimeConfig
	dispatcher *Dispatcher
	bindings   []Binding
	proxies    map[string]*mtglib.Proxy // keyed by FQDN

	mu sync.Mutex
}

// NewRuntime creates the runtime without starting it.
func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Runtime{cfg: cfg, proxies: map[string]*mtglib.Proxy{}}
}

// Bindings returns a snapshot of the currently-resolved bindings. Safe for
// read-only use by callers (e.g., the status endpoint or the Telegram bot).
func (r *Runtime) Bindings() []Binding {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]Binding, len(r.bindings))
	copy(cp, r.bindings)
	return cp
}

// Start loads or generates each secret, builds mtglib proxies, registers them
// with the dispatcher, and starts the dispatcher.
func (r *Runtime) Start(ctx context.Context) error {
	// Parse loopback into host/port — each mtglib.Proxy needs the fronting
	// target set as IP + int port.
	host, portStr, err := net.SplitHostPort(r.cfg.Dispatcher.Loopback)
	if err != nil {
		return fmt.Errorf("mtproto runtime: invalid loopback %q: %w", r.cfg.Dispatcher.Loopback, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("mtproto runtime: invalid loopback port %q", portStr)
	}

	dispatcherCfg := r.cfg.Dispatcher
	if dispatcherCfg.Logger == nil {
		dispatcherCfg.Logger = r.cfg.Logger
	}
	disp, err := NewDispatcher(dispatcherCfg)
	if err != nil {
		return err
	}
	r.dispatcher = disp

	// Shared mtglib plumbing reused across all proxies. Anti-replay cache is
	// per-runtime so replay protection spans all bindings.
	mtgLogger := NewSlogLogger(r.cfg.Logger.With("component", "mtglib"))
	net4, err := network.GetDNS(nil)
	if err != nil {
		return fmt.Errorf("mtproto runtime: DNS resolver: %w", err)
	}
	ntw := network.New(
		net4, "",
		10*time.Second, // tcp
		10*time.Second, // http
		5*time.Minute,  // idle
		net.KeepAliveConfig{Enable: true, Idle: 30 * time.Second, Interval: 10 * time.Second, Count: 9},
	)

	var cache mtglib.AntiReplayCache
	if r.cfg.AntiReplayCacheSize == 0 {
		cache = antireplay.NewStableBloomFilter(
			antireplay.DefaultStableBloomFilterMaxSize,
			antireplay.DefaultStableBloomFilterErrorRate,
		)
	} else {
		cache = antireplay.NewStableBloomFilter(
			r.cfg.AntiReplayCacheSize,
			antireplay.DefaultStableBloomFilterErrorRate,
		)
	}
	blocklist := ipblocklist.NewNoop()
	allowlist := ipblocklist.NewNoop()
	eventStream := events.NewNoopStream()

	resolvedBindings := make([]Binding, 0, len(r.cfg.Bindings))
	handlers := make(map[string]ConnHandler, len(r.cfg.Bindings))
	for _, sb := range r.cfg.Bindings {
		b, err := r.cfg.Store.Load(sb.Subdomain, sb.FQDN)
		if err != nil {
			return fmt.Errorf("mtproto runtime: load %s: %w", sb.Subdomain, err)
		}
		proxy, err := mtglib.NewProxy(mtglib.ProxyOpts{
			Secret:             b.Secret,
			Network:            ntw,
			AntiReplayCache:    cache,
			IPBlocklist:        blocklist,
			IPAllowlist:        allowlist,
			EventStream:        eventStream,
			Logger:             mtgLogger.Named(b.Subdomain),
			Concurrency:        uint(dispatcherCfg.MaxConnections),
			DomainFrontingPort: uint(port),
			DomainFrontingIP:   host,
		})
		if err != nil {
			return fmt.Errorf("mtproto runtime: new proxy for %s: %w", b.FQDN, err)
		}
		r.proxies[b.FQDN] = proxy
		handlers[b.FQDN] = proxy
		resolvedBindings = append(resolvedBindings, b)
	}

	disp.UpdateBindings(handlers)
	r.mu.Lock()
	r.bindings = resolvedBindings
	r.mu.Unlock()

	if err := disp.Start(ctx); err != nil {
		// Dispose proxies we already built — they own background workers.
		r.shutdownProxies()
		return err
	}
	return nil
}

// Shutdown stops accepting new connections and tears down proxies.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r.dispatcher != nil {
		_ = r.dispatcher.Shutdown(ctx)
	}
	r.shutdownProxies()
	return nil
}

func (r *Runtime) shutdownProxies() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.proxies {
		p.Shutdown()
	}
	r.proxies = map[string]*mtglib.Proxy{}
}
