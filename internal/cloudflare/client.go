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
	api     *cloudflare.API
	zoneID  string
	domain  string
	proxied bool // Cloudflare proxy mode (orange cloud)
	ttl     int  // DNS record TTL in seconds

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
		proxied:     cfg.CloudflareProxy,
		ttl:         cfg.DNSTTL,
		recordCache: make(map[string]string),
	}, nil
}

// validateRecordName ensures the record name is within the configured domain scope.
// This is a safety assertion to prevent accidental modifications to records outside the domain.
func (c *Client) validateRecordName(name string) error {
	// Normalize both to lowercase for comparison
	normalizedName := strings.ToLower(strings.TrimSuffix(name, "."))
	normalizedDomain := strings.ToLower(strings.TrimSuffix(c.domain, "."))

	// Record must either be the domain itself or a subdomain of it
	if normalizedName != normalizedDomain && !strings.HasSuffix(normalizedName, "."+normalizedDomain) {
		return fmt.Errorf("SECURITY: record name %q is outside configured domain %q - refusing to modify", name, c.domain)
	}

	slog.Debug("Record name validation passed", "name", name, "domain", c.domain)
	return nil
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
