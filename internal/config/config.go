package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the dyndns service
type Config struct {
	// Cloudflare settings
	CloudflareAPIToken string
	CloudflareZoneID   string
	CloudflareProxy    bool // Enable Cloudflare proxy (orange cloud)

	// DNS settings
	DNSTTL int // TTL for DNS records in seconds

	// Domain settings
	Domain          string
	AcmeEmail       string
	SubdomainPrefix bool // Use prefix mode (app-zone.example.com instead of app.zone.example.com)

	// CatchallSubdomain, when non-empty, enables a dedicated 451 site block.
	// Any TLS handshake whose SNI does not match a configured site lands on
	// this site's Let's Encrypt cert (via default_sni) and receives a 451.
	// Leave empty to disable the feature (legacy behavior).
	CatchallSubdomain string

	// MTProtoDispatcher, when true, runs an MTProto FakeTLS dispatcher on
	// port 443 in front of Caddy. Caddy is moved to a loopback listener
	// (see MTProtoCaddyPort). Non-MTProto traffic is forwarded byte-for-byte.
	MTProtoDispatcher bool

	// MTProtoSubdomains lists the subdomains (relative to Domain/BaseDomain)
	// that should be bound to MTProto. Each listed subdomain gets:
	//   * A grey-cloud A/AAAA record (direct mode).
	//   * A Caddy site block that responds "OK" 200 to browser traffic.
	//   * A persistent MTProto secret stored under MTProtoDataDir.
	//   * An mtglib.Proxy instance keyed by SNI inside the dispatcher.
	// Ignored when MTProtoDispatcher is false.
	MTProtoSubdomains []string

	// MTProtoDataDir is where generated MTProto secrets are persisted (one
	// file per subdomain). Defaults to ${DataDir}/mtproto.
	MTProtoDataDir string

	// MTProtoDispatcherBind is the "host:port" that the MTProto dispatcher
	// listens on when enabled. Defaults to ":443" (all interfaces).
	MTProtoDispatcherBind string

	// MTProtoCaddyLoopback is the "host:port" that the dispatcher forwards
	// non-MTProto traffic to — i.e. where the Caddy HTTPS listener binds
	// when the dispatcher is enabled. Defaults to "127.0.0.1:8443".
	MTProtoCaddyLoopback string

	// MTProtoMaxConnections caps the dispatcher's in-flight connection count
	// across all bound domains. Connections above this limit are closed
	// immediately. Defaults to 8192.
	MTProtoMaxConnections int

	// TelegramBotToken is the Bot API token (from BotFather). When non-empty,
	// the bot runs: long-polls getUpdates, handles /status and /rotate from
	// allow-listed users in DMs, and pushes notifications to TelegramBotChatIDs.
	TelegramBotToken string

	// TelegramBotChatIDs lists the chat IDs that receive secret-generation
	// and rotation notifications. Negative IDs address group/supergroup chats.
	TelegramBotChatIDs []int64

	// TelegramBotAllowedUsers are the Telegram user IDs permitted to issue
	// commands (/status, /rotate) in a private chat with the bot. Empty
	// means no user can run commands; the bot still broadcasts to
	// TelegramBotChatIDs.
	TelegramBotAllowedUsers []int64

	// Fritzbox settings for TR-064/UPnP
	FritzboxHost     string
	FritzboxUser     string
	FritzboxPassword string

	// Manual IP override
	ManualIPv4 string
	ManualIPv6 string

	// Timing
	IPCheckInterval time.Duration

	// Logging
	LogLevel string

	// Paths
	DataDir      string
	LogsDir      string
	SharedDir    string
	MappingsFile string
	CaddyFile    string

	// Stevedore discovery settings
	StevedoreSocket string
	StevedoreToken  string
}

// Load reads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		CloudflareAPIToken: os.Getenv("CLOUDFLARE_API_TOKEN"),
		CloudflareZoneID:   os.Getenv("CLOUDFLARE_ZONE_ID"),
		Domain:             os.Getenv("DOMAIN"),
		AcmeEmail:          os.Getenv("ACME_EMAIL"),
		FritzboxHost:       getEnvDefault("FRITZBOX_HOST", "192.168.178.1"),
		FritzboxUser:       os.Getenv("FRITZBOX_USER"),
		FritzboxPassword:   os.Getenv("FRITZBOX_PASSWORD"),
		ManualIPv4:         os.Getenv("MANUAL_IPV4"),
		ManualIPv6:         os.Getenv("MANUAL_IPV6"),
		LogLevel:           getEnvDefault("LOG_LEVEL", "info"),
		DataDir:            getEnvDefault("DYNDNS_DATA", "/data"),
		LogsDir:            getEnvDefault("DYNDNS_LOGS", "/var/log/dyndns"),
		SharedDir:          getEnvDefault("STEVEDORE_SHARED", "/shared"),
		StevedoreSocket:    getEnvDefault("STEVEDORE_SOCKET", "/var/run/stevedore/query.sock"),
		StevedoreToken:     os.Getenv("STEVEDORE_TOKEN"),
	}

	// Parse IP check interval
	intervalStr := getEnvDefault("IP_CHECK_INTERVAL", "5m")
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return nil, fmt.Errorf("invalid IP_CHECK_INTERVAL: %w", err)
	}
	cfg.IPCheckInterval = interval

	// Parse Cloudflare proxy mode
	cfg.CloudflareProxy = parseBool(os.Getenv("CLOUDFLARE_PROXY"))

	// Parse subdomain prefix mode (for Cloudflare Universal SSL compatibility)
	cfg.SubdomainPrefix = parseBool(os.Getenv("SUBDOMAIN_PREFIX"))

	// Parse catchall subdomain (optional; enables the 451 catchall site).
	cfg.CatchallSubdomain = os.Getenv("CATCHALL_SUBDOMAIN")

	// Parse MTProto dispatcher configuration.
	cfg.MTProtoDispatcher = parseBool(os.Getenv("MTPROTO_DISPATCHER"))
	cfg.MTProtoSubdomains = parseCommaList(os.Getenv("MTPROTO_SUBDOMAINS"))
	cfg.MTProtoDataDir = os.Getenv("MTPROTO_DATA_DIR")
	cfg.MTProtoDispatcherBind = getEnvDefault("MTPROTO_DISPATCHER_BIND", ":443")
	cfg.MTProtoCaddyLoopback = getEnvDefault("MTPROTO_CADDY_LOOPBACK", "127.0.0.1:8443")
	if v := os.Getenv("MTPROTO_MAX_CONNECTIONS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MTPROTO_MAX_CONNECTIONS: %q", v)
		}
		cfg.MTProtoMaxConnections = n
	} else {
		cfg.MTProtoMaxConnections = 8192
	}

	// Telegram bot parameters.
	cfg.TelegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if ids, err := parseInt64List(os.Getenv("TELEGRAM_BOT_CHAT_IDS")); err != nil {
		return nil, fmt.Errorf("invalid TELEGRAM_BOT_CHAT_IDS: %w", err)
	} else {
		cfg.TelegramBotChatIDs = ids
	}
	if ids, err := parseInt64List(os.Getenv("TELEGRAM_BOT_ALLOWED_USERS")); err != nil {
		return nil, fmt.Errorf("invalid TELEGRAM_BOT_ALLOWED_USERS: %w", err)
	} else {
		cfg.TelegramBotAllowedUsers = ids
	}

	// Parse DNS TTL (default to IP check interval in seconds, minimum 60)
	if ttlStr := os.Getenv("DNS_TTL"); ttlStr != "" {
		ttl, err := strconv.Atoi(ttlStr)
		if err != nil {
			return nil, fmt.Errorf("invalid DNS_TTL: %w", err)
		}
		if ttl < 60 {
			ttl = 60 // Cloudflare minimum for non-proxied records
		}
		cfg.DNSTTL = ttl
	} else {
		// Default to IP check interval (in seconds), minimum 60
		ttl := int(interval.Seconds())
		if ttl < 60 {
			ttl = 60
		}
		cfg.DNSTTL = ttl
	}

	// Set derived paths - prefer shared directory for cross-deployment communication
	// Check shared dir first (Stevedore standard), fallback to data dir
	sharedMappings := cfg.SharedDir + "/dyndns-mappings.yaml"
	dataMappings := cfg.DataDir + "/mappings.yaml"

	// Use explicit MAPPINGS_FILE if set, otherwise prefer shared location
	if mappingsFile := os.Getenv("MAPPINGS_FILE"); mappingsFile != "" {
		cfg.MappingsFile = mappingsFile
	} else if fileExists(sharedMappings) {
		cfg.MappingsFile = sharedMappings
	} else if fileExists(dataMappings) {
		cfg.MappingsFile = dataMappings
	} else {
		// Default to shared location for new installations
		cfg.MappingsFile = sharedMappings
	}

	cfg.CaddyFile = "/etc/caddy/Caddyfile"

	// Derive MTProto data dir now that DataDir is known.
	if cfg.MTProtoDataDir == "" {
		cfg.MTProtoDataDir = cfg.DataDir + "/mtproto"
	}

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that all required configuration is present
func (c *Config) Validate() error {
	if c.CloudflareAPIToken == "" {
		return fmt.Errorf("CLOUDFLARE_API_TOKEN is required")
	}
	if c.CloudflareZoneID == "" {
		return fmt.Errorf("CLOUDFLARE_ZONE_ID is required")
	}
	if c.Domain == "" {
		return fmt.Errorf("DOMAIN is required")
	}
	if c.AcmeEmail == "" {
		return fmt.Errorf("ACME_EMAIL is required")
	}
	return nil
}

// UseManualIP returns true if manual IP configuration is set
func (c *Config) UseManualIP() bool {
	return c.ManualIPv4 != "" || c.ManualIPv6 != ""
}

// UseDiscovery returns true if stevedore discovery is configured
func (c *Config) UseDiscovery() bool {
	return c.StevedoreToken != ""
}

// GetSubdomainFQDN returns the full domain name for a subdomain label.
// If the argument already contains a dot it is treated as a fully qualified
// hostname and returned verbatim — this lets MTProto bindings declare
// sibling zones like zone451.example.com without being mangled by
// prefix-mode substitution.
// In prefix mode: subdomain-basedomain.parent.com (e.g., app-zone.example.com)
// In normal mode: subdomain.domain (e.g., app.zone.example.com)
func (c *Config) GetSubdomainFQDN(subdomain string) string {
	if strings.Contains(subdomain, ".") {
		return subdomain
	}
	if c.SubdomainPrefix {
		// Extract the parent domain (everything after first dot)
		parts := strings.SplitN(c.Domain, ".", 2)
		if len(parts) == 2 {
			// Convert subdomain.zone -> subdomain-zone.parent
			return subdomain + "-" + parts[0] + "." + parts[1]
		}
		// Fallback for single-part domains
		return subdomain + "-" + c.Domain
	}
	return subdomain + "." + c.Domain
}

// ResolveMTProtoEntry interprets a single MTPROTO_SUBDOMAINS entry and
// returns the (label, fqdn) pair used throughout the MTProto subsystem.
//
// If the entry contains a dot it is treated as an FQDN verbatim — this lets
// operators bind hostnames that are siblings of the zone (e.g.
// zone451.example.com when DOMAIN is zone33.example.com), which
// prefix-mode substitution would otherwise mangle into zone451-zone33.....
// The label (used for on-disk filenames, /rotate arguments, fingerprinting)
// is the leftmost DNS label.
//
// If the entry contains no dot it is treated as a short subdomain label and
// passed through GetSubdomainFQDN — preserving the original behavior for
// single-label MTPROTO_SUBDOMAINS like "mtp".
func (c *Config) ResolveMTProtoEntry(entry string) (label, fqdn string) {
	entry = strings.TrimSpace(entry)
	if strings.Contains(entry, ".") {
		label = entry
		if idx := strings.Index(entry, "."); idx > 0 {
			label = entry[:idx]
		}
		return label, entry
	}
	return entry, c.GetSubdomainFQDN(entry)
}

// GetBaseDomain returns the parent domain for DNS record creation in prefix mode.
// In prefix mode, subdomains like app-zone.example.com are direct children of example.com.
// In normal mode, returns the configured domain.
// For single-level domains (like example.com), returns the domain as-is since there's no valid parent.
func (c *Config) GetBaseDomain() string {
	if c.SubdomainPrefix {
		parts := strings.SplitN(c.Domain, ".", 2)
		// Only use parent if it has at least 2 parts (e.g., example.com, not just "com")
		if len(parts) == 2 && strings.Contains(parts[1], ".") {
			return parts[1]
		}
	}
	return c.Domain
}

func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// parseBool parses common boolean string representations
func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "1" || s == "yes" || s == "on"
}

// parseInt64List parses a comma-separated list of int64 values. Returns
// nil for an empty input. Trims whitespace and ignores empty entries.
func parseInt64List(s string) ([]int64, error) {
	parts := parseCommaList(s)
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("entry %q: %w", p, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// parseCommaList splits a comma-separated string, trims whitespace, and
// drops empty entries. Returns nil for an empty input.
func parseCommaList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
