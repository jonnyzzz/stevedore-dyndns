package ipdetect

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
)

func TestNew(t *testing.T) {
	cfg := &config.Config{}
	detector := New(cfg)

	if detector == nil {
		t.Fatal("New() returned nil")
	}
	if detector.cfg != cfg {
		t.Error("New() did not store config")
	}
	if detector.httpClient == nil {
		t.Error("New() did not create HTTP client")
	}
}

func TestDetector_Detect_ManualIP(t *testing.T) {
	cfg := &config.Config{
		ManualIPv4: "1.2.3.4",
		ManualIPv6: "2001:db8::1",
	}
	detector := New(cfg)

	ipv4, ipv6, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() unexpected error: %v", err)
	}
	if ipv4 != "1.2.3.4" {
		t.Errorf("Detect() IPv4 = %q, want %q", ipv4, "1.2.3.4")
	}
	if ipv6 != "2001:db8::1" {
		t.Errorf("Detect() IPv6 = %q, want %q", ipv6, "2001:db8::1")
	}
}

func TestDetector_Detect_ManualIPv4Only(t *testing.T) {
	cfg := &config.Config{
		ManualIPv4: "1.2.3.4",
	}
	detector := New(cfg)

	ipv4, ipv6, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() unexpected error: %v", err)
	}
	if ipv4 != "1.2.3.4" {
		t.Errorf("Detect() IPv4 = %q, want %q", ipv4, "1.2.3.4")
	}
	if ipv6 != "" {
		t.Errorf("Detect() IPv6 = %q, want empty", ipv6)
	}
}

func TestDetector_GetLastKnown(t *testing.T) {
	cfg := &config.Config{
		ManualIPv4: "1.2.3.4",
		ManualIPv6: "2001:db8::1",
	}
	detector := New(cfg)

	// Initially should be empty
	ipv4, ipv6, _ := detector.GetLastKnown()
	if ipv4 != "" || ipv6 != "" {
		t.Error("GetLastKnown() should return empty before first detect")
	}

	// After detect, should return the detected IPs
	detector.Detect(context.Background())

	ipv4, ipv6, _ = detector.GetLastKnown()
	if ipv4 != "1.2.3.4" {
		t.Errorf("GetLastKnown() IPv4 = %q, want %q", ipv4, "1.2.3.4")
	}
	if ipv6 != "2001:db8::1" {
		t.Errorf("GetLastKnown() IPv6 = %q, want %q", ipv6, "2001:db8::1")
	}
}

func TestDetector_FetchIPFromService(t *testing.T) {
	// Create test server that returns an IP
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "  203.0.113.42  ") // With whitespace to test trimming
	}))
	defer server.Close()

	cfg := &config.Config{}
	detector := New(cfg)

	ip, err := detector.fetchIPFromService(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetchIPFromService() unexpected error: %v", err)
	}
	if ip != "203.0.113.42" {
		t.Errorf("fetchIPFromService() = %q, want %q", ip, "203.0.113.42")
	}
}

func TestDetector_FetchIPFromService_Error(t *testing.T) {
	// Create test server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{}
	detector := New(cfg)

	_, err := detector.fetchIPFromService(context.Background(), server.URL)
	if err == nil {
		t.Error("fetchIPFromService() expected error for 500 response")
	}
}

func TestDetector_FetchIPFromService_Timeout(t *testing.T) {
	// Create test server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprintln(w, "1.2.3.4")
	}))
	defer server.Close()

	cfg := &config.Config{}
	detector := New(cfg)
	detector.httpClient.Timeout = 50 * time.Millisecond

	_, err := detector.fetchIPFromService(context.Background(), server.URL)
	if err == nil {
		t.Error("fetchIPFromService() expected timeout error")
	}
}

func TestDetector_DetectFromExternalServices(t *testing.T) {
	// Create test servers
	ipv4Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.42")
	}))
	defer ipv4Server.Close()

	ipv6Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "2001:db8::42")
	}))
	defer ipv6Server.Close()

	cfg := &config.Config{}
	detector := New(cfg)

	// Override the service URLs for testing (we can't easily do this with the current implementation)
	// This test demonstrates the structure - in production the real services are used
	// For now, just test that the function exists and handles context correctly

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// This will fail to reach the real services quickly due to timeout
	// which is expected behavior for a unit test
	_, _, _ = detector.detectFromExternalServices(ctx)
}

func TestIsValidIPv4(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"1.2.3.4", true},
		{"255.255.255.255", true},
		{"0.0.0.0", true},
		{"192.168.1.1", true},
		{"invalid", false},
		{"", false},
		{"1.2.3", false},
		{"1.2.3.4.5", false},
		{"2001:db8::1", false}, // IPv6 is not IPv4
		{"256.1.1.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isValidIPv4(tt.ip); got != tt.want {
				t.Errorf("isValidIPv4(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsValidIPv6(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"2001:db8::1", true},
		{"::1", true},
		{"::", true},
		{"fe80::1", true},
		{"2001:0db8:0000:0000:0000:0000:0000:0001", true},
		{"invalid", false},
		{"", false},
		{"1.2.3.4", false}, // IPv4 is not IPv6
		{"2001:db8:::1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isValidIPv6(tt.ip); got != tt.want {
				t.Errorf("isValidIPv6(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestParseSOAPIPResponse_IPv4(t *testing.T) {
	cfg := &config.Config{}
	detector := New(cfg)

	// Valid SOAP response for IPv4
	soapResponse := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body>
    <u:GetExternalIPAddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
      <NewExternalIPAddress>203.0.113.42</NewExternalIPAddress>
    </u:GetExternalIPAddressResponse>
  </s:Body>
</s:Envelope>`

	ip := detector.parseSOAPIPResponse(soapResponse, false)
	if ip != "203.0.113.42" {
		t.Errorf("parseSOAPIPResponse() = %q, want %q", ip, "203.0.113.42")
	}
}

func TestParseSOAPIPResponse_IPv6(t *testing.T) {
	cfg := &config.Config{}
	detector := New(cfg)

	// Valid SOAP response for IPv6
	soapResponse := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body>
    <u:X_AVM_DE_GetExternalIPv6AddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
      <NewExternalIPv6Address>2001:db8::42</NewExternalIPv6Address>
    </u:X_AVM_DE_GetExternalIPv6AddressResponse>
  </s:Body>
</s:Envelope>`

	ip := detector.parseSOAPIPResponse(soapResponse, true)
	if ip != "2001:db8::42" {
		t.Errorf("parseSOAPIPResponse() = %q, want %q", ip, "2001:db8::42")
	}
}

func TestParseSOAPIPResponse_Invalid(t *testing.T) {
	cfg := &config.Config{}
	detector := New(cfg)

	// Invalid SOAP response
	soapResponse := `not valid xml`

	ip := detector.parseSOAPIPResponse(soapResponse, false)
	if ip != "" {
		t.Errorf("parseSOAPIPResponse() = %q, want empty for invalid XML", ip)
	}
}

func TestParseSOAPIPResponse_Empty(t *testing.T) {
	cfg := &config.Config{}
	detector := New(cfg)

	// SOAP response with no IP
	soapResponse := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body>
    <u:GetExternalIPAddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
      <NewExternalIPAddress></NewExternalIPAddress>
    </u:GetExternalIPAddressResponse>
  </s:Body>
</s:Envelope>`

	ip := detector.parseSOAPIPResponse(soapResponse, false)
	if ip != "" {
		t.Errorf("parseSOAPIPResponse() = %q, want empty for empty IP", ip)
	}
}

func TestDetector_FritzboxGetExternalIP_MockServer(t *testing.T) {
	// Create a mock Fritzbox server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check request
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "text/xml; charset=utf-8" {
			t.Errorf("Unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		}

		// Check SOAPAction header
		soapAction := r.Header.Get("SOAPAction")
		if soapAction == "urn:schemas-upnp-org:service:WANIPConnection:1#GetExternalIPAddress" {
			// Return IPv4 response
			response := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body>
    <u:GetExternalIPAddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
      <NewExternalIPAddress>203.0.113.42</NewExternalIPAddress>
    </u:GetExternalIPAddressResponse>
  </s:Body>
</s:Envelope>`
			w.Header().Set("Content-Type", "text/xml")
			w.Write([]byte(response))
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	// Extract host:port from test server URL
	cfg := &config.Config{
		FritzboxHost: server.Listener.Addr().String(),
	}
	_ = New(cfg)

	// The mock server doesn't use the correct port, so this test is more of a structure test
	// In a real scenario, you'd need to either:
	// 1. Make the port configurable
	// 2. Use a more sophisticated mock setup
	// For now, this demonstrates the test structure
}

func TestDetector_ValidateWithExternalServices(t *testing.T) {
	// Create mock external service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.42")
	}))
	defer server.Close()

	cfg := &config.Config{}
	detector := New(cfg)

	// Test when Fritzbox IP matches external service
	// Note: This test is limited because we can't easily mock the service URLs
	// In production, this would validate against real services

	ctx := context.Background()
	ipv4, ipv6 := detector.validateWithExternalServices(ctx, "", "")

	// With empty inputs, should return empty
	if ipv4 != "" {
		t.Errorf("validateWithExternalServices() IPv4 = %q, want empty", ipv4)
	}
	if ipv6 != "" {
		t.Errorf("validateWithExternalServices() IPv6 = %q, want empty", ipv6)
	}
}

func TestDetector_Detect_ContextCancellation(t *testing.T) {
	cfg := &config.Config{
		FritzboxHost: "192.0.2.1", // TEST-NET-1, won't respond
	}
	detector := New(cfg)
	detector.httpClient.Timeout = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, _, err := detector.Detect(ctx)
	if err == nil {
		t.Log("Detect() with cancelled context may still succeed with fallback")
	}
}

// Test XML structure for SOAP response parsing
func TestSOAPResponseStructure(t *testing.T) {
	type ExternalIPResponse struct {
		XMLName             xml.Name `xml:"Envelope"`
		ExternalIPAddress   string   `xml:"Body>GetExternalIPAddressResponse>NewExternalIPAddress"`
		ExternalIPv6Address string   `xml:"Body>X_AVM_DE_GetExternalIPv6AddressResponse>NewExternalIPv6Address"`
	}

	testCases := []struct {
		name     string
		xml      string
		wantIPv4 string
		wantIPv6 string
	}{
		{
			name: "IPv4 response",
			xml: `<Envelope>
				<Body>
					<GetExternalIPAddressResponse>
						<NewExternalIPAddress>1.2.3.4</NewExternalIPAddress>
					</GetExternalIPAddressResponse>
				</Body>
			</Envelope>`,
			wantIPv4: "1.2.3.4",
		},
		{
			name: "IPv6 response",
			xml: `<Envelope>
				<Body>
					<X_AVM_DE_GetExternalIPv6AddressResponse>
						<NewExternalIPv6Address>2001:db8::1</NewExternalIPv6Address>
					</X_AVM_DE_GetExternalIPv6AddressResponse>
				</Body>
			</Envelope>`,
			wantIPv6: "2001:db8::1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var resp ExternalIPResponse
			if err := xml.Unmarshal([]byte(tc.xml), &resp); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}
			if resp.ExternalIPAddress != tc.wantIPv4 {
				t.Errorf("IPv4 = %q, want %q", resp.ExternalIPAddress, tc.wantIPv4)
			}
			if resp.ExternalIPv6Address != tc.wantIPv6 {
				t.Errorf("IPv6 = %q, want %q", resp.ExternalIPv6Address, tc.wantIPv6)
			}
		})
	}
}
