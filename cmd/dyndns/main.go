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

	// Mapping manager
	mappingMgr := mapping.New(cfg.MappingsFile)

	// Caddy config generator
	caddyGen := caddy.New(cfg, mappingMgr)

	// Start the main control loop
	go runControlLoop(ctx, cfg, detector, cfClient, caddyGen, mappingMgr)

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
) {
	// Initial IP detection and DNS update
	updateIPAndDNS(ctx, cfg, detector, cfClient)

	// Generate initial Caddy config
	if err := caddyGen.Generate(); err != nil {
		slog.Error("Failed to generate Caddy config", "error", err)
	}

	// Watch for mapping changes
	go mappingMgr.Watch(ctx, func() {
		slog.Info("Mappings changed, regenerating Caddy config")
		if err := caddyGen.Generate(); err != nil {
			slog.Error("Failed to regenerate Caddy config", "error", err)
		}
	})

	// Periodic IP check
	ticker := time.NewTicker(cfg.IPCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateIPAndDNS(ctx, cfg, detector, cfClient)
		}
	}
}

func updateIPAndDNS(
	ctx context.Context,
	cfg *config.Config,
	detector *ipdetect.Detector,
	cfClient *cloudflare.Client,
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

	// Update DNS records
	if ipv4 != "" {
		if err := cfClient.UpdateRecord(ctx, cfg.Domain, "A", ipv4); err != nil {
			slog.Error("Failed to update A record", "error", err)
		} else {
			slog.Info("Updated A record", "domain", cfg.Domain, "ip", ipv4)
		}

		// Update wildcard A record
		if err := cfClient.UpdateRecord(ctx, "*."+cfg.Domain, "A", ipv4); err != nil {
			slog.Error("Failed to update wildcard A record", "error", err)
		} else {
			slog.Info("Updated wildcard A record", "domain", "*."+cfg.Domain, "ip", ipv4)
		}
	}

	if ipv6 != "" {
		if err := cfClient.UpdateRecord(ctx, cfg.Domain, "AAAA", ipv6); err != nil {
			slog.Error("Failed to update AAAA record", "error", err)
		} else {
			slog.Info("Updated AAAA record", "domain", cfg.Domain, "ip", ipv6)
		}

		// Update wildcard AAAA record
		if err := cfClient.UpdateRecord(ctx, "*."+cfg.Domain, "AAAA", ipv6); err != nil {
			slog.Error("Failed to update wildcard AAAA record", "error", err)
		} else {
			slog.Info("Updated wildcard AAAA record", "domain", "*."+cfg.Domain, "ip", ipv6)
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
		w.Write([]byte("OK"))
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
		server.Shutdown(context.Background())
	}()

	slog.Info("Starting status server", "addr", ":8081")
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Status server error", "error", err)
	}
}
