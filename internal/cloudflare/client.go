package cloudflare

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/cloudflare/cloudflare-go"
	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
)

// Client wraps the Cloudflare API client
type Client struct {
	api        *cloudflare.API
	zoneID     string
	domain     string
	baseDomain string // Parent domain in prefix mode
	proxied    bool   // Cloudflare proxy mode (orange cloud)
	ttl        int    // DNS record TTL in seconds

	// Cache of record IDs to avoid lookups
	recordCache map[string]string
	cacheMu     sync.RWMutex
}

// New creates a new Cloudflare client
func New(cfg *config.Config) (*Client, error) {
	api, err := cloudflare.NewWithAPIToken(cfg.CloudflareAPIToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloudflare client: %w", err)
	}

	return &Client{
		api:         api,
		zoneID:      cfg.CloudflareZoneID,
		domain:      cfg.Domain,
		baseDomain:  cfg.GetBaseDomain(),
		proxied:     cfg.CloudflareProxy,
		ttl:         cfg.DNSTTL,
		recordCache: make(map[string]string),
	}, nil
}

// validateRecordName ensures the record name is within the configured domain scope.
// This is a safety assertion to prevent accidental modifications to records outside the domain.
// In prefix mode, records may be subdomains of baseDomain (e.g., app-zone.example.com when domain is zone.example.com)
func (c *Client) validateRecordName(name string) error {
	// Normalize to lowercase for comparison
	normalizedName := strings.ToLower(strings.TrimSuffix(name, "."))
	normalizedDomain := strings.ToLower(strings.TrimSuffix(c.domain, "."))
	normalizedBaseDomain := strings.ToLower(strings.TrimSuffix(c.baseDomain, "."))

	// Check against configured domain (normal mode)
	if normalizedName == normalizedDomain || strings.HasSuffix(normalizedName, "."+normalizedDomain) {
		slog.Debug("Record name validation passed (domain match)", "name", name, "domain", c.domain)
		return nil
	}

	// Check against base domain (prefix mode - allows app-zone.example.com when domain is zone.example.com)
	if normalizedBaseDomain != "" && normalizedBaseDomain != normalizedDomain {
		if normalizedName == normalizedBaseDomain || strings.HasSuffix(normalizedName, "."+normalizedBaseDomain) {
			slog.Debug("Record name validation passed (baseDomain match)", "name", name, "baseDomain", c.baseDomain)
			return nil
		}
	}

	return fmt.Errorf("SECURITY: record name %q is outside configured domain %q (baseDomain: %q) - refusing to modify", name, c.domain, c.baseDomain)
}

// UpdateRecord creates or updates a DNS record
func (c *Client) UpdateRecord(ctx context.Context, name string, recordType string, content string) error {
	// SECURITY ASSERTION: Ensure we only modify records within our domain
	if err := c.validateRecordName(name); err != nil {
		return err
	}

	cacheKey := fmt.Sprintf("%s:%s", name, recordType)

	// Check cache for existing record ID
	c.cacheMu.RLock()
	recordID, cached := c.recordCache[cacheKey]
	c.cacheMu.RUnlock()

	rc := cloudflare.ZoneIdentifier(c.zoneID)

	if !cached {
		// Look up existing record
		records, _, err := c.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
			Name: name,
			Type: recordType,
		})
		if err != nil {
			return fmt.Errorf("failed to list DNS records: %w", err)
		}

		if len(records) > 0 {
			recordID = records[0].ID
			c.cacheMu.Lock()
			c.recordCache[cacheKey] = recordID
			c.cacheMu.Unlock()
		}
	}

	// Cloudflare uses TTL=1 for "automatic" when proxied
	ttl := c.ttl
	if c.proxied {
		ttl = 1 // Automatic TTL when proxied
	}

	if recordID != "" {
		// Update existing record
		_, err := c.api.UpdateDNSRecord(ctx, rc, cloudflare.UpdateDNSRecordParams{
			ID:      recordID,
			Type:    recordType,
			Name:    name,
			Content: content,
			TTL:     ttl,
			Proxied: cloudflare.BoolPtr(c.proxied),
		})
		if err != nil {
			return fmt.Errorf("failed to update DNS record: %w", err)
		}
		slog.Debug("Updated DNS record", "name", name, "type", recordType, "content", content, "ttl", ttl, "proxied", c.proxied)
	} else {
		// Create new record
		record, err := c.api.CreateDNSRecord(ctx, rc, cloudflare.CreateDNSRecordParams{
			Type:    recordType,
			Name:    name,
			Content: content,
			TTL:     ttl,
			Proxied: cloudflare.BoolPtr(c.proxied),
		})
		if err != nil {
			return fmt.Errorf("failed to create DNS record: %w", err)
		}
		c.cacheMu.Lock()
		c.recordCache[cacheKey] = record.ID
		c.cacheMu.Unlock()
		slog.Debug("Created DNS record", "name", name, "type", recordType, "content", content, "id", record.ID, "ttl", ttl, "proxied", c.proxied)
	}

	return nil
}

// DeleteRecord removes a DNS record
func (c *Client) DeleteRecord(ctx context.Context, name string, recordType string) error {
	// SECURITY ASSERTION: Ensure we only delete records within our domain
	if err := c.validateRecordName(name); err != nil {
		return err
	}

	cacheKey := fmt.Sprintf("%s:%s", name, recordType)

	c.cacheMu.RLock()
	recordID, cached := c.recordCache[cacheKey]
	c.cacheMu.RUnlock()

	rc := cloudflare.ZoneIdentifier(c.zoneID)

	if !cached {
		// Look up existing record
		records, _, err := c.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
			Name: name,
			Type: recordType,
		})
		if err != nil {
			return fmt.Errorf("failed to list DNS records: %w", err)
		}
		if len(records) == 0 {
			return nil // Record doesn't exist
		}
		recordID = records[0].ID
	}

	if err := c.api.DeleteDNSRecord(ctx, rc, recordID); err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}

	c.cacheMu.Lock()
	delete(c.recordCache, cacheKey)
	c.cacheMu.Unlock()

	slog.Debug("Deleted DNS record", "name", name, "type", recordType)
	return nil
}

// GetZoneInfo returns information about the configured zone
func (c *Client) GetZoneInfo(ctx context.Context) (*cloudflare.Zone, error) {
	zone, err := c.api.ZoneDetails(ctx, c.zoneID)
	if err != nil {
		return nil, fmt.Errorf("failed to get zone details: %w", err)
	}
	return &zone, nil
}

// IsProxied returns whether Cloudflare proxy mode is enabled
func (c *Client) IsProxied() bool {
	return c.proxied
}

// Domain returns the configured domain
func (c *Client) Domain() string {
	return c.domain
}

// SetSSLMode sets the SSL/TLS encryption mode for the zone.
// Valid values: "off", "flexible", "full", "strict" (for Full Strict)
// For Cloudflare proxy mode, "full" or "strict" is required to connect to origin on port 443.
func (c *Client) SetSSLMode(ctx context.Context, mode string) error {
	rc := cloudflare.ZoneIdentifier(c.zoneID)

	_, err := c.api.UpdateZoneSetting(ctx, rc, cloudflare.UpdateZoneSettingParams{
		Name:  "ssl",
		Value: mode,
	})
	if err != nil {
		return fmt.Errorf("failed to set SSL mode to %q: %w", mode, err)
	}

	slog.Info("Set Cloudflare SSL mode", "mode", mode, "zone_id", c.zoneID)
	return nil
}

// GetSSLMode returns the current SSL/TLS encryption mode for the zone.
func (c *Client) GetSSLMode(ctx context.Context) (string, error) {
	rc := cloudflare.ZoneIdentifier(c.zoneID)

	setting, err := c.api.GetZoneSetting(ctx, rc, cloudflare.GetZoneSettingParams{
		Name: "ssl",
	})
	if err != nil {
		return "", fmt.Errorf("failed to get SSL mode: %w", err)
	}

	if mode, ok := setting.Value.(string); ok {
		return mode, nil
	}
	return "", fmt.Errorf("unexpected SSL mode value type: %T", setting.Value)
}

// SetAuthenticatedOriginPull enables or disables Authenticated Origin Pull (mTLS).
// When enabled, Cloudflare presents a client certificate when connecting to the origin.
// The origin should validate this certificate to ensure requests come from Cloudflare.
func (c *Client) SetAuthenticatedOriginPull(ctx context.Context, enabled bool) error {
	rc := cloudflare.ZoneIdentifier(c.zoneID)

	value := "off"
	if enabled {
		value = "on"
	}

	_, err := c.api.UpdateZoneSetting(ctx, rc, cloudflare.UpdateZoneSettingParams{
		Name:  "tls_client_auth",
		Value: value,
	})
	if err != nil {
		return fmt.Errorf("failed to set Authenticated Origin Pull to %q: %w", value, err)
	}

	slog.Info("Set Cloudflare Authenticated Origin Pull", "enabled", enabled, "zone_id", c.zoneID)
	return nil
}

// IsAuthenticatedOriginPullEnabled returns whether Authenticated Origin Pull is enabled.
func (c *Client) IsAuthenticatedOriginPullEnabled(ctx context.Context) (bool, error) {
	rc := cloudflare.ZoneIdentifier(c.zoneID)

	setting, err := c.api.GetZoneSetting(ctx, rc, cloudflare.GetZoneSettingParams{
		Name: "tls_client_auth",
	})
	if err != nil {
		return false, fmt.Errorf("failed to get Authenticated Origin Pull status: %w", err)
	}

	if value, ok := setting.Value.(string); ok {
		return value == "on", nil
	}
	return false, fmt.Errorf("unexpected tls_client_auth value type: %T", setting.Value)
}

// ConfigureForProxyMode ensures Cloudflare is properly configured for proxy mode.
// It sets SSL mode to "full" and enables Authenticated Origin Pull.
func (c *Client) ConfigureForProxyMode(ctx context.Context) error {
	// Set SSL mode to "full" (connects to origin on port 443)
	// Using "full" instead of "strict" because origin may use self-signed or Cloudflare Origin CA certs
	if err := c.SetSSLMode(ctx, "full"); err != nil {
		return fmt.Errorf("failed to set SSL mode: %w", err)
	}

	// Enable Authenticated Origin Pull (mTLS)
	if err := c.SetAuthenticatedOriginPull(ctx, true); err != nil {
		return fmt.Errorf("failed to enable Authenticated Origin Pull: %w", err)
	}

	return nil
}

// GetManagedSubdomainRecords returns all subdomain DNS records managed by this service.
// It looks for A and AAAA records that are subdomains of the configured domain.
func (c *Client) GetManagedSubdomainRecords(ctx context.Context) ([]string, error) {
	rc := cloudflare.ZoneIdentifier(c.zoneID)

	// Get all A records
	aRecords, _, err := c.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
		Type: "A",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list A records: %w", err)
	}

	// Get all AAAA records
	aaaaRecords, _, err := c.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
		Type: "AAAA",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list AAAA records: %w", err)
	}

	// Collect subdomain names (not the domain itself, not wildcards)
	seen := make(map[string]bool)
	var subdomains []string

	for _, r := range append(aRecords, aaaaRecords...) {
		name := strings.TrimSuffix(r.Name, ".")
		domain := strings.TrimSuffix(c.domain, ".")

		// Skip the domain itself
		if strings.EqualFold(name, domain) {
			continue
		}

		// Skip wildcard records
		if strings.HasPrefix(name, "*.") {
			continue
		}

		// Only include subdomains of our domain
		if !strings.HasSuffix(strings.ToLower(name), "."+strings.ToLower(domain)) {
			continue
		}

		// Extract just the subdomain part
		subdomain := strings.TrimSuffix(strings.ToLower(name), "."+strings.ToLower(domain))
		if subdomain != "" && !seen[subdomain] {
			seen[subdomain] = true
			subdomains = append(subdomains, subdomain)
		}
	}

	return subdomains, nil
}
