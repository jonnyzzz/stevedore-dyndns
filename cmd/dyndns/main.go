package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jonnyzzz/stevedore-dyndns/internal/caddy"
	"github.com/jonnyzzz/stevedore-dyndns/internal/cloudflare"
	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
	"github.com/jonnyzzz/stevedore-dyndns/internal/discovery"
	"github.com/jonnyzzz/stevedore-dyndns/internal/ipdetect"
	"github.com/jonnyzzz/stevedore-dyndns/internal/mapping"
	"github.com/jonnyzzz/stevedore-dyndns/internal/mtproto"
	"github.com/jonnyzzz/stevedore-dyndns/internal/telegram"
)

// Build-time variables injected via ldflags
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Setup logging
	logLevel := os.Getenv("LOG_LEVEL")
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	slog.Info("Starting stevedore-dyndns",
		"version", Version,
		"commit", GitCommit,
		"build_date", BuildDate,
		"log_level", logLevel,
	)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	slog.Info("Configuration loaded",
		"domain", cfg.Domain,
		"fritzbox_host", cfg.FritzboxHost,
		"ip_check_interval", cfg.IPCheckInterval,
		"use_discovery", cfg.UseDiscovery(),
	)

	// Initialize components
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// IP detector
	detector := ipdetect.New(cfg)

	// Cloudflare client
	cfClient, err := cloudflare.New(cfg)
	if err != nil {
		slog.Error("Failed to initialize Cloudflare client", "error", err)
		os.Exit(1)
	}

	// Configure Cloudflare for proxy mode if enabled
	if cfg.CloudflareProxy {
		slog.Info("Cloudflare proxy mode enabled, configuring SSL and mTLS...")
		if err := cfClient.ConfigureForProxyMode(ctx); err != nil {
			slog.Error("Failed to configure Cloudflare for proxy mode", "error", err)
			// Don't exit - this might fail if token doesn't have zone settings permissions
			// The service can still work, it just won't auto-configure Cloudflare
		}
	}

	// Mapping manager (for backwards compatibility with YAML files)
	var mappingMgr *mapping.Manager
	if !cfg.UseDiscovery() {
		mappingMgr = mapping.New(cfg.MappingsFile)
	}

	// Caddy config generator
	caddyGen := caddy.New(cfg, mappingMgr)

	// Discovery client (if configured)
	var discoveryClient *discovery.Client
	if cfg.UseDiscovery() {
		discoveryClient = discovery.New(discovery.Config{
			SocketPath: cfg.StevedoreSocket,
			Token:      cfg.StevedoreToken,
		})
		slog.Info("Discovery mode enabled", "socket", cfg.StevedoreSocket)
	}

	// MTProto dispatcher (optional) — binds :443 and forwards non-MTProto
	// traffic to Caddy on the configured loopback port.
	var mtprotoRuntime *mtproto.Runtime
	var mtprotoStore *mtproto.Store
	if cfg.MTProtoDispatcher {
		rt, store, err := startMTProtoDispatcher(ctx, cfg, logger)
		if err != nil {
			slog.Error("Failed to start MTProto dispatcher", "error", err)
			os.Exit(1)
		}
		mtprotoRuntime = rt
		mtprotoStore = store
		logMTProtoBindings(rt, cfg)
	}

	// Telegram bot (optional). Runs in its own goroutine; broadcasts the
	// generated bindings on startup so the admin has an easy copy path.
	// The prior URL message in each chat is deleted first so chats don't
	// accumulate stale posts across restarts / rotations.
	if cfg.TelegramBotToken != "" {
		bot, err := newTelegramBot(cfg, mtprotoRuntime, mtprotoStore, logger, cancel)
		if err != nil {
			slog.Error("Failed to construct Telegram bot", "error", err)
			os.Exit(1)
		}
		go func() {
			if err := bot.Run(ctx); err != nil {
				slog.Error("Telegram bot exited with error", "error", err)
			}
		}()
		if mtprotoRuntime != nil {
			for _, b := range mtprotoRuntime.Bindings() {
				text := fmt.Sprintf(
					"MTProto binding ready for %s (fp=%s)\n%s",
					b.FQDN, b.Fingerprint(), b.TelegramURL(),
				)
				bot.Post(ctx, "binding:"+b.FQDN, text)
			}
		}
	}

	// Start the main control loop
	go runControlLoop(ctx, cfg, detector, cfClient, caddyGen, mappingMgr, discoveryClient)

	// Start HTTP status server
	go runStatusServer(ctx, cfg, detector, cfClient, mtprotoRuntime)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutting down...")
	cancel()
	if mtprotoRuntime != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mtprotoRuntime.Shutdown(shutdownCtx)
		shutdownCancel()
	}
	time.Sleep(time.Second) // Grace period
	slog.Info("Goodbye!")
}

// startMTProtoDispatcher builds the runtime from config, starts it, and
// returns the handle for status/Shutdown along with the secret store so
// the Telegram bot can rotate secrets.
func startMTProtoDispatcher(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*mtproto.Runtime, *mtproto.Store, error) {
	if len(cfg.MTProtoSubdomains) == 0 {
		return nil, nil, fmt.Errorf("MTPROTO_DISPATCHER is true but MTPROTO_SUBDOMAINS is empty")
	}
	store, err := mtproto.NewStore(cfg.MTProtoDataDir)
	if err != nil {
		return nil, nil, err
	}
	bindings := make([]mtproto.SubdomainBinding, 0, len(cfg.MTProtoSubdomains))
	for _, entry := range cfg.MTProtoSubdomains {
		label, fqdn := cfg.ResolveMTProtoEntry(entry)
		bindings = append(bindings, mtproto.SubdomainBinding{
			Subdomain: label,
			FQDN:      fqdn,
		})
	}
	rt := mtproto.NewRuntime(mtproto.RuntimeConfig{
		Bindings: bindings,
		Store:    store,
		Dispatcher: mtproto.DispatcherConfig{
			Listen:           cfg.MTProtoDispatcherBind,
			Loopback:         cfg.MTProtoCaddyLoopback,
			MaxConnections:   cfg.MTProtoMaxConnections,
			HandshakeTimeout: 10 * time.Second,
			Logger:           logger.With("component", "mtproto"),
		},
		Logger: logger,
	})
	if err := rt.Start(ctx); err != nil {
		return nil, nil, err
	}
	return rt, store, nil
}

// telegramHandlers adapts mtproto.Runtime + mtproto.Store to
// telegram.Handlers. It provides /status and /rotate semantics to the bot.
type telegramHandlers struct {
	cfg     *config.Config
	runtime *mtproto.Runtime
	store   *mtproto.Store
	// post posts a rotation notification through the bot's dedup-aware
	// path. The caller supplies a "kind" so repeat posts about the same
	// binding collapse into one live chat message.
	post func(ctx context.Context, kind, text string)
}

func (h *telegramHandlers) Status() []telegram.Binding {
	if h.runtime == nil {
		return nil
	}
	src := h.runtime.Bindings()
	out := make([]telegram.Binding, 0, len(src))
	for _, b := range src {
		out = append(out, telegram.Binding{
			Subdomain:   b.Subdomain,
			FQDN:        b.FQDN,
			Fingerprint: b.Fingerprint(),
			TelegramURL: b.TelegramURL(),
		})
	}
	return out
}

func (h *telegramHandlers) Rotate(subdomain string) (telegram.Binding, error) {
	if h.runtime == nil || h.store == nil {
		return telegram.Binding{}, fmt.Errorf("MTProto dispatcher not running")
	}
	var fqdn string
	found := false
	for _, b := range h.runtime.Bindings() {
		if b.Subdomain == subdomain || b.FQDN == subdomain {
			fqdn = b.FQDN
			subdomain = b.Subdomain
			found = true
			break
		}
	}
	if !found {
		return telegram.Binding{}, fmt.Errorf("no MTProto binding for subdomain %q", subdomain)
	}
	b, err := h.store.Rotate(subdomain, fqdn)
	if err != nil {
		return telegram.Binding{}, err
	}
	return telegram.Binding{
		Subdomain:   b.Subdomain,
		FQDN:        b.FQDN,
		Fingerprint: b.Fingerprint(),
		TelegramURL: b.TelegramURL(),
	}, nil
}

func (h *telegramHandlers) NotifyRotated(b telegram.Binding) {
	if h.post == nil {
		return
	}
	// Same kind as the startup binding announcement — the rotation message
	// overwrites the prior URL post for this FQDN instead of stacking a new
	// one on top.
	h.post(context.Background(), "binding:"+b.FQDN, fmt.Sprintf(
		"MTProto secret rotated for %s (fp=%s)\n%s",
		b.FQDN, b.Fingerprint, b.TelegramURL,
	))
}

// newTelegramBot constructs the bot with a concrete HTTP API client and an
// adapter over the MTProto runtime + store. The restart callback cancels
// the root context so Stevedore can restart dyndns with the new secret.
//
// A persistent MessageStore keeps track of the "current URL post" per chat
// so subsequent broadcasts delete the prior message, keeping the chat tidy
// across service restarts (e.g. after /rotate).
func newTelegramBot(cfg *config.Config, rt *mtproto.Runtime, store *mtproto.Store, logger *slog.Logger, cancel context.CancelFunc) (*telegram.Bot, error) {
	api := telegram.NewHTTPAPI(cfg.TelegramBotToken, nil)
	msgPath := cfg.MTProtoDataDir + "/telegram_last_msgs.json"
	msgStore, err := telegram.NewMessageStore(msgPath)
	if err != nil {
		return nil, fmt.Errorf("telegram message store: %w", err)
	}
	handlers := &telegramHandlers{cfg: cfg, runtime: rt, store: store}
	bot := telegram.NewBot(api, handlers, cfg.TelegramBotChatIDs, logger.With("component", "telegram"), cancel, msgStore)
	// Wire the rotation callback through the bot's kind-aware Post so that
	// the rotation notification overwrites the prior binding message rather
	// than accumulating in the chat.
	handlers.post = bot.Post
	return bot, nil
}

// logMTProtoBindings emits a one-time INFO entry per bound subdomain so the
// admin can locate the generated secret files. The secret itself is NOT
// logged — only the file path and a short fingerprint.
func logMTProtoBindings(rt *mtproto.Runtime, cfg *config.Config) {
	for _, b := range rt.Bindings() {
		slog.Info("MTProto binding active",
			"subdomain", b.Subdomain,
			"fqdn", b.FQDN,
			"fingerprint", b.Fingerprint(),
			"secret_file", cfg.MTProtoDataDir+"/"+b.Subdomain+".secret",
			"tg_link_file", cfg.MTProtoDataDir+"/"+b.Subdomain+".tg",
		)
	}
}

func runControlLoop(
	ctx context.Context,
	cfg *config.Config,
	detector *ipdetect.Detector,
	cfClient *cloudflare.Client,
	caddyGen *caddy.Generator,
	mappingMgr *mapping.Manager,
	discoveryClient *discovery.Client,
) {
	// Load initial services/mappings BEFORE IP update (so subdomains are known)
	var initialServices []discovery.Service
	if discoveryClient != nil {
		// Discovery mode: fetch services from stevedore socket
		services, err := discoveryClient.GetIngressServices(ctx)
		if err != nil {
			slog.Error("Failed to fetch initial services from discovery", "error", err)
		} else {
			slog.Info("Loaded services from discovery", "count", len(services))
			caddyGen.UpdateDiscoveredServices(services)
			initialServices = append([]discovery.Service(nil), services...)
		}
	} else if mappingMgr != nil {
		// Legacy mode: load mappings from YAML file
		if err := mappingMgr.Load(); err != nil {
			slog.Error("Failed to load initial mappings", "error", err)
		}
	}

	// Generate initial Caddy config
	if err := caddyGen.Generate(); err != nil {
		slog.Error("Failed to generate Caddy config", "error", err)
	}

	// Initial IP detection and DNS update (after discovery, so subdomains are known)
	updateIPAndDNS(ctx, cfg, detector, cfClient, caddyGen)

	// Start service discovery polling or file watching
	if discoveryClient != nil {
		go runDiscoveryLoop(ctx, discoveryClient, caddyGen, initialServices)
	} else if mappingMgr != nil {
		go mappingMgr.Watch(ctx, func() {
			slog.Info("Mappings changed, regenerating Caddy config")
			if err := caddyGen.Generate(); err != nil {
				slog.Error("Failed to regenerate Caddy config", "error", err)
			}
		})
	}

	// Periodic IP check
	ticker := time.NewTicker(cfg.IPCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateIPAndDNS(ctx, cfg, detector, cfClient, caddyGen)
		}
	}
}

// runDiscoveryLoop polls the stevedore socket for service changes
func runDiscoveryLoop(ctx context.Context, client *discovery.Client, caddyGen *caddy.Generator, lastServices []discovery.Service) {
	var since time.Time

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		services, newSince, err := client.Poll(ctx, since)
		if err != nil {
			slog.Error("Discovery poll failed", "error", err)
			// Wait before retrying on error
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		since = newSince

		// If services changed (not nil), update and regenerate
		if services != nil {
			if discovery.ServicesEqual(services, lastServices) {
				slog.Debug("Discovery poll returned unchanged services, skipping Caddy reload", "count", len(services))
				continue
			}
			slog.Info("Services changed via discovery", "count", len(services))
			caddyGen.UpdateDiscoveredServices(services)
			lastServices = append([]discovery.Service(nil), services...)
			if err := caddyGen.Generate(); err != nil {
				slog.Error("Failed to regenerate Caddy config", "error", err)
			}
		}
	}
}

func updateIPAndDNS(
	ctx context.Context,
	cfg *config.Config,
	detector *ipdetect.Detector,
	cfClient *cloudflare.Client,
	caddyGen *caddy.Generator,
) {
	// Detect current IPs
	ipv4, ipv6, err := detector.Detect(ctx)
	if err != nil {
		slog.Error("Failed to detect IP addresses", "error", err)
		return
	}

	slog.Info("Detected IP addresses",
		"ipv4", ipv4,
		"ipv6", ipv6,
	)

	// Handle DNS records based on proxy mode
	if cfClient.IsProxied() {
		// Proxy mode: Only update individual subdomain records
		// We don't need root domain records in proxy mode - only the specific
		// subdomains that services are using get DNS records
		slog.Debug("Proxy mode: skipping root domain DNS records, updating subdomains only")
	} else {
		// Direct mode: Update root domain DNS records
		if ipv4 != "" {
			if err := cfClient.UpdateRecord(ctx, cfg.Domain, "A", ipv4); err != nil {
				slog.Error("Failed to update A record", "error", err)
			} else {
				slog.Info("Updated A record", "domain", cfg.Domain, "ip", ipv4)
			}
		}

		if ipv6 != "" {
			if err := cfClient.UpdateRecord(ctx, cfg.Domain, "AAAA", ipv6); err != nil {
				slog.Error("Failed to update AAAA record", "error", err)
			} else {
				slog.Info("Updated AAAA record", "domain", cfg.Domain, "ip", ipv6)
			}
		}
	}

	// Handle subdomain records based on proxy mode
	if cfClient.IsProxied() {
		// Proxy mode: create individual subdomain records (required for Cloudflare Universal SSL)
		updateSubdomainRecords(ctx, cfg, cfClient, caddyGen, ipv4, ipv6)
	} else {
		// Direct mode: use wildcard records
		if ipv4 != "" {
			if err := cfClient.UpdateRecord(ctx, "*."+cfg.Domain, "A", ipv4); err != nil {
				slog.Error("Failed to update wildcard A record", "error", err)
			} else {
				slog.Info("Updated wildcard A record", "domain", "*."+cfg.Domain, "ip", ipv4)
			}
		}
		if ipv6 != "" {
			if err := cfClient.UpdateRecord(ctx, "*."+cfg.Domain, "AAAA", ipv6); err != nil {
				slog.Error("Failed to update wildcard AAAA record", "error", err)
			} else {
				slog.Info("Updated wildcard AAAA record", "domain", "*."+cfg.Domain, "ip", ipv6)
			}
		}
	}
}

// updateSubdomainRecords creates/updates individual subdomain DNS records.
// This is required when Cloudflare proxy is enabled because Cloudflare Universal SSL
// doesn't cover wildcard subdomains (*.domain.com).
//
// Mixed mode: subdomains marked direct in stevedore discovery are published as
// grey-cloud (Proxied=false) so Caddy can terminate TLS with its own LE cert.
// Other subdomains keep orange-cloud (Proxied=true) behavior, and IPv6 is
// handled by Cloudflare edge automatically — only A records are emitted for
// proxied subdomains. Direct subdomains additionally receive AAAA records when
// an IPv6 address is known, because clients connect to the origin directly.
func updateSubdomainRecords(
	ctx context.Context,
	cfg *config.Config,
	cfClient *cloudflare.Client,
	caddyGen *caddy.Generator,
	ipv4, ipv6 string,
) {
	// Get active subdomains from Caddy config
	activeSubdomains := caddyGen.GetActiveSubdomains()

	// The 451 catchall always behaves as direct-mode: its own LE cert, grey-cloud.
	catchallSub := cfg.CatchallSubdomain
	if catchallSub != "" {
		found := false
		for _, s := range activeSubdomains {
			if s == catchallSub {
				found = true
				break
			}
		}
		if !found {
			activeSubdomains = append(activeSubdomains, catchallSub)
		}
	}

	// Create a set for quick lookup (stores normalized FQDNs for case-insensitive comparison)
	activeFQDNs := make(map[string]bool)
	for _, sub := range activeSubdomains {
		fqdn := strings.ToLower(cfg.GetSubdomainFQDN(sub))
		activeFQDNs[fqdn] = true
	}

	slog.Info("Updating subdomain DNS records",
		"prefix_mode", cfg.SubdomainPrefix,
		"active_subdomains", len(activeSubdomains),
		"catchall", catchallSub,
	)

	for _, subdomain := range activeSubdomains {
		fqdn := cfg.GetSubdomainFQDN(subdomain)
		direct := caddyGen.IsSubdomainDirect(subdomain) || subdomain == catchallSub
		proxied := !direct

		if ipv4 != "" {
			if err := cfClient.UpdateRecordProxied(ctx, fqdn, "A", ipv4, proxied); err != nil {
				slog.Error("Failed to update subdomain A record", "subdomain", subdomain, "fqdn", fqdn, "direct", direct, "error", err)
			} else {
				slog.Info("Updated subdomain A record", "subdomain", subdomain, "fqdn", fqdn, "direct", direct)
			}
		}

		// AAAA records only make sense when the client reaches the origin directly.
		// In proxied mode Cloudflare provides IPv6 to clients while connecting to
		// the origin over IPv4; adding an AAAA would expose the origin's IPv6.
		if direct && ipv6 != "" {
			if err := cfClient.UpdateRecordProxied(ctx, fqdn, "AAAA", ipv6, false); err != nil {
				slog.Error("Failed to update subdomain AAAA record", "subdomain", subdomain, "fqdn", fqdn, "error", err)
			} else {
				slog.Info("Updated subdomain AAAA record", "subdomain", subdomain, "fqdn", fqdn)
			}
		}
	}

	// Clean up old subdomain records that are no longer active (terraform-like reconciliation)
	// Get all FQDNs from Cloudflare that belong to this deployment
	existingFQDNs, err := cfClient.GetManagedRecordFQDNs(ctx)
	if err != nil {
		slog.Error("Failed to get existing DNS records", "error", err)
		return
	}

	slog.Debug("DNS reconciliation",
		"existing_fqdns", len(existingFQDNs),
		"active_fqdns", len(activeFQDNs),
	)

	// Delete records that exist in Cloudflare but shouldn't (stale records)
	for _, existingFQDN := range existingFQDNs {
		// Normalize for comparison
		normalizedFQDN := strings.ToLower(existingFQDN)

		if !activeFQDNs[normalizedFQDN] {
			slog.Info("Removing stale DNS record", "fqdn", existingFQDN)

			if err := cfClient.DeleteRecord(ctx, existingFQDN, "A"); err != nil {
				slog.Error("Failed to delete stale A record", "fqdn", existingFQDN, "error", err)
			}
			// Also clean up any stale AAAA records from previous configurations
			if err := cfClient.DeleteRecord(ctx, existingFQDN, "AAAA"); err != nil {
				slog.Error("Failed to delete stale AAAA record", "fqdn", existingFQDN, "error", err)
			}
		}
	}
}

func runStatusServer(
	ctx context.Context,
	cfg *config.Config,
	detector *ipdetect.Detector,
	cfClient *cloudflare.Client,
	mtprotoRuntime *mtproto.Runtime,
) {
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Status endpoint
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		ipv4, ipv6, _ := detector.GetLastKnown()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ipv4": %q, "ipv6": %q, "domain": %q`, ipv4, ipv6, cfg.Domain)
		if mtprotoRuntime != nil {
			fmt.Fprint(w, `, "mtproto": [`)
			first := true
			for _, b := range mtprotoRuntime.Bindings() {
				if !first {
					fmt.Fprint(w, ",")
				}
				first = false
				fmt.Fprintf(w, `{"subdomain":%q,"fqdn":%q,"fingerprint":%q}`,
					b.Subdomain, b.FQDN, b.Fingerprint())
			}
			fmt.Fprint(w, `]`)
		}
		fmt.Fprint(w, `}`)
	})

	server := &http.Server{
		Addr:    "127.0.0.1:8081",
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	slog.Info("Starting status server", "addr", "127.0.0.1:8081")
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Status server error", "error", err)
	}
}
