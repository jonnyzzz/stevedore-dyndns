package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jonnyzzz/stevedore-dyndns/internal/caddy"
	"github.com/jonnyzzz/stevedore-dyndns/internal/cloudflare"
	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
	"github.com/jonnyzzz/stevedore-dyndns/internal/discovery"
	"github.com/jonnyzzz/stevedore-dyndns/internal/ipdetect"
	"github.com/jonnyzzz/stevedore-dyndns/internal/mapping"
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
		"version", "0.1.0",
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

	// Start the main control loop
	go runControlLoop(ctx, cfg, detector, cfClient, caddyGen, mappingMgr, discoveryClient)

	// Start HTTP status server
	go runStatusServer(ctx, cfg, detector, cfClient)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutting down...")
	cancel()
	time.Sleep(time.Second) // Grace period
	slog.Info("Goodbye!")
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
	if discoveryClient != nil {
		// Discovery mode: fetch services from stevedore socket
		services, err := discoveryClient.GetIngressServices(ctx)
		if err != nil {
			slog.Error("Failed to fetch initial services from discovery", "error", err)
		} else {
			slog.Info("Loaded services from discovery", "count", len(services))
			caddyGen.UpdateDiscoveredServices(services)
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
		go runDiscoveryLoop(ctx, discoveryClient, caddyGen)
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
func runDiscoveryLoop(ctx context.Context, client *discovery.Client, caddyGen *caddy.Generator) {
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
			slog.Info("Services changed via discovery", "count", len(services))
			caddyGen.UpdateDiscoveredServices(services)
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

// updateSubdomainRecords creates/updates individual subdomain DNS records
// This is required when Cloudflare proxy is enabled because Cloudflare Universal SSL
// doesn't cover wildcard subdomains (*.domain.com)
//
// In proxy mode, we only create A records (IPv4) - Cloudflare automatically provides
// IPv6 connectivity to clients via their edge network. This avoids issues where the
// origin doesn't have IPv6 port forwarding configured.
func updateSubdomainRecords(
	ctx context.Context,
	cfg *config.Config,
	cfClient *cloudflare.Client,
	caddyGen *caddy.Generator,
	ipv4, ipv6 string,
) {
	// Get active subdomains from Caddy config
	activeSubdomains := caddyGen.GetActiveSubdomains()

	// Create a set for quick lookup (stores FQDNs)
	activeFQDNs := make(map[string]bool)
	for _, sub := range activeSubdomains {
		fqdn := cfg.GetSubdomainFQDN(sub)
		activeFQDNs[fqdn] = true
	}

	slog.Info("Updating subdomain DNS records",
		"proxy_mode", true,
		"prefix_mode", cfg.SubdomainPrefix,
		"active_subdomains", len(activeSubdomains),
	)

	// Update records for each active subdomain
	// In proxy mode: only A records - Cloudflare handles IPv6 for clients automatically
	for _, subdomain := range activeSubdomains {
		fqdn := cfg.GetSubdomainFQDN(subdomain)

		if ipv4 != "" {
			if err := cfClient.UpdateRecord(ctx, fqdn, "A", ipv4); err != nil {
				slog.Error("Failed to update subdomain A record", "subdomain", subdomain, "fqdn", fqdn, "error", err)
			} else {
				slog.Info("Updated subdomain A record", "subdomain", subdomain, "fqdn", fqdn)
			}
		}

		// Note: We intentionally skip AAAA records for subdomains in proxy mode.
		// Cloudflare's proxy automatically provides IPv6 connectivity to clients
		// while communicating with origin over IPv4 only. This avoids issues where
		// home routers don't have IPv6 port forwarding configured.
	}

	// Clean up old subdomain records that are no longer active
	existingSubdomains, err := cfClient.GetManagedSubdomainRecords(ctx)
	if err != nil {
		slog.Error("Failed to get existing subdomain records", "error", err)
		return
	}

	for _, existing := range existingSubdomains {
		// Construct FQDN for the existing record
		existingFQDN := existing + "." + cfClient.Domain()

		if !activeFQDNs[existingFQDN] {
			slog.Info("Removing stale subdomain DNS record", "subdomain", existing, "fqdn", existingFQDN)

			if err := cfClient.DeleteRecord(ctx, existingFQDN, "A"); err != nil {
				slog.Error("Failed to delete stale A record", "subdomain", existing, "error", err)
			}
			// Also clean up any stale AAAA records from previous configurations
			if err := cfClient.DeleteRecord(ctx, existingFQDN, "AAAA"); err != nil {
				slog.Error("Failed to delete stale AAAA record", "subdomain", existing, "error", err)
			}
		}
	}
}

func runStatusServer(
	ctx context.Context,
	cfg *config.Config,
	detector *ipdetect.Detector,
	cfClient *cloudflare.Client,
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
		fmt.Fprintf(w, `{"ipv4": %q, "ipv6": %q, "domain": %q}`, ipv4, ipv6, cfg.Domain)
	})

	server := &http.Server{
		Addr:    ":8081",
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	slog.Info("Starting status server", "addr", ":8081")
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Status server error", "error", err)
	}
}
