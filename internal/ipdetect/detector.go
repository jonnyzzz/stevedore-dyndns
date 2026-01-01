package ipdetect

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
)

// Detector handles IP address detection
type Detector struct {
	cfg *config.Config

	lastIPv4 string
	lastIPv6 string
	lastMu   sync.RWMutex

	httpClient *http.Client
}

// New creates a new IP detector
func New(cfg *config.Config) *Detector {
	return &Detector{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Detect returns the current public IPv4 and IPv6 addresses
func (d *Detector) Detect(ctx context.Context) (ipv4, ipv6 string, err error) {
	// Check for manual override
	if d.cfg.UseManualIP() {
		slog.Debug("Using manual IP configuration")
		ipv4 = d.cfg.ManualIPv4
		ipv6 = d.cfg.ManualIPv6
		d.updateLast(ipv4, ipv6)
		return ipv4, ipv6, nil
	}

	// Try Fritzbox TR-064 first
	ipv4, ipv6, err = d.detectFromFritzbox(ctx)
	if err == nil && (ipv4 != "" || ipv6 != "") {
		slog.Debug("Got IP from Fritzbox", "ipv4", ipv4, "ipv6", ipv6)
		d.updateLast(ipv4, ipv6)
		return ipv4, ipv6, nil
	}
	if err != nil {
		slog.Warn("Fritzbox detection failed", "error", err)
	}

	// Fallback to external services
	ipv4, ipv6, err = d.detectFromExternalServices(ctx)
	if err != nil {
		return "", "", fmt.Errorf("all IP detection methods failed: %w", err)
	}

	d.updateLast(ipv4, ipv6)
	return ipv4, ipv6, nil
}

// GetLastKnown returns the last detected IP addresses
func (d *Detector) GetLastKnown() (ipv4, ipv6 string, err error) {
	d.lastMu.RLock()
	defer d.lastMu.RUnlock()
	return d.lastIPv4, d.lastIPv6, nil
}

func (d *Detector) updateLast(ipv4, ipv6 string) {
	d.lastMu.Lock()
	defer d.lastMu.Unlock()
	d.lastIPv4 = ipv4
	d.lastIPv6 = ipv6
}

// detectFromFritzbox uses TR-064 SOAP protocol to get external IP
func (d *Detector) detectFromFritzbox(ctx context.Context) (ipv4, ipv6 string, err error) {
	host := d.cfg.FritzboxHost

	// Get IPv4 via WANIPConnection service
	ipv4, err = d.fritzboxGetExternalIP(ctx, host, false)
	if err != nil {
		slog.Debug("Failed to get IPv4 from Fritzbox", "error", err)
	}

	// Get IPv6 via WANIPConnection service
	ipv6, err = d.fritzboxGetExternalIP(ctx, host, true)
	if err != nil {
		slog.Debug("Failed to get IPv6 from Fritzbox", "error", err)
	}

	if ipv4 == "" && ipv6 == "" {
		return "", "", fmt.Errorf("no IP addresses retrieved from Fritzbox")
	}

	return ipv4, ipv6, nil
}

func (d *Detector) fritzboxGetExternalIP(ctx context.Context, host string, isIPv6 bool) (string, error) {
	// TR-064 SOAP envelope for GetExternalIPAddress
	soapAction := "urn:schemas-upnp-org:service:WANIPConnection:1#GetExternalIPAddress"
	if isIPv6 {
		soapAction = "urn:schemas-upnp-org:service:WANIPConnection:1#X_AVM_DE_GetExternalIPv6Address"
	}

	soapBody := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:GetExternalIPAddress xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"/>
  </s:Body>
</s:Envelope>`

	if isIPv6 {
		soapBody = `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:X_AVM_DE_GetExternalIPv6Address xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"/>
  </s:Body>
</s:Envelope>`
	}

	url := fmt.Sprintf("http://%s:49000/igdupnp/control/WANIPConn1", host)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader([]byte(soapBody)))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)

	// Add authentication if configured
	if d.cfg.FritzboxUser != "" && d.cfg.FritzboxPassword != "" {
		req.SetBasicAuth(d.cfg.FritzboxUser, d.cfg.FritzboxPassword)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Parse SOAP response
	ip := d.parseSOAPIPResponse(string(body), isIPv6)
	if ip == "" {
		return "", fmt.Errorf("no IP found in response")
	}

	return ip, nil
}

func (d *Detector) parseSOAPIPResponse(body string, isIPv6 bool) string {
	// Simple XML parsing for the IP address
	type ExternalIPResponse struct {
		XMLName             xml.Name `xml:"Envelope"`
		ExternalIPAddress   string   `xml:"Body>GetExternalIPAddressResponse>NewExternalIPAddress"`
		ExternalIPv6Address string   `xml:"Body>X_AVM_DE_GetExternalIPv6AddressResponse>NewExternalIPv6Address"`
	}

	var response ExternalIPResponse
	if err := xml.Unmarshal([]byte(body), &response); err != nil {
		slog.Debug("Failed to parse SOAP response", "error", err)
		return ""
	}

	if isIPv6 {
		return response.ExternalIPv6Address
	}
	return response.ExternalIPAddress
}

// detectFromExternalServices uses public IP detection services as fallback
func (d *Detector) detectFromExternalServices(ctx context.Context) (ipv4, ipv6 string, err error) {
	slog.Info("Falling back to external IP detection services")

	// IPv4 detection services
	ipv4Services := []string{
		"https://api.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://v4.ident.me",
	}

	// IPv6 detection services
	ipv6Services := []string{
		"https://api6.ipify.org",
		"https://ipv6.icanhazip.com",
		"https://v6.ident.me",
	}

	// Try IPv4
	for _, svc := range ipv4Services {
		ip, err := d.fetchIPFromService(ctx, svc)
		if err == nil && isValidIPv4(ip) {
			ipv4 = ip
			break
		}
	}

	// Try IPv6
	for _, svc := range ipv6Services {
		ip, err := d.fetchIPFromService(ctx, svc)
		if err == nil && isValidIPv6(ip) {
			ipv6 = ip
			break
		}
	}

	if ipv4 == "" && ipv6 == "" {
		return "", "", fmt.Errorf("could not detect any IP address")
	}

	return ipv4, ipv6, nil
}

func (d *Detector) fetchIPFromService(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
}

func isValidIPv4(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.To4() != nil
}

func isValidIPv6(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.To4() == nil
}
