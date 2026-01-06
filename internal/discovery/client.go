package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Service represents a service discovered via stevedore labels.
type Service struct {
	// Deployment is the stevedore deployment name
	Deployment string `json:"deployment"`
	// Container is the Docker container name
	Container string `json:"container"`
	// Subdomain is the subdomain to route to this service
	Subdomain string `json:"subdomain"`
	// Port is the container port (HTTP)
	Port int `json:"port"`
	// Websocket indicates if WebSocket support is needed
	Websocket bool `json:"websocket"`
	// HealthCheck is the health check path (optional)
	HealthCheck string `json:"healthCheck"`
}

// Client queries the stevedore socket API for service discovery.
type Client struct {
	socketPath string
	token      string
	httpClient *http.Client
}

// Config holds configuration for the discovery client.
type Config struct {
	SocketPath string
	Token      string
}

// New creates a new discovery client.
func New(cfg Config) *Client {
	// Create HTTP client that uses Unix socket
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("unix", cfg.SocketPath)
		},
	}

	return &Client{
		socketPath: cfg.SocketPath,
		token:      cfg.Token,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   70 * time.Second, // Slightly longer than poll timeout
		},
	}
}

// ingressConfig represents the structured ingress configuration from stevedore API.
type ingressConfig struct {
	Enabled     bool   `json:"enabled"`
	Subdomain   string `json:"subdomain"`
	Port        int    `json:"port"`
	Websocket   bool   `json:"websocket,omitempty"`
	Healthcheck string `json:"healthcheck,omitempty"`
}

// serviceResponse matches the stevedore API response structure.
type serviceResponse struct {
	Deployment    string            `json:"deployment"`
	Service       string            `json:"service"`
	ContainerID   string            `json:"container_id"`
	ContainerName string            `json:"container_name"`
	Running       bool              `json:"running"`
	Ingress       *ingressConfig    `json:"ingress,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"` // Legacy format
}

// GetIngressServices returns all services with ingress labels.
func (c *Client) GetIngressServices(ctx context.Context) ([]Service, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://stevedore/services?ingress=true", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query services: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var svcResponses []serviceResponse
	if err := json.NewDecoder(resp.Body).Decode(&svcResponses); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return c.parseServices(svcResponses), nil
}

// EventType represents the type of change event from stevedore.
type EventType string

const (
	EventDeploymentCreated       EventType = "deployment.created"
	EventDeploymentUpdated       EventType = "deployment.updated"
	EventDeploymentRemoved       EventType = "deployment.removed"
	EventDeploymentStatusChanged EventType = "deployment.status_changed"
	EventParamsChanged           EventType = "params.changed"
)

// Event represents a change event from stevedore.
type Event struct {
	Type       EventType         `json:"type"`
	Deployment string            `json:"deployment,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
	Details    map[string]string `json:"details,omitempty"`
}

// pollResponse matches the stevedore poll API response.
type pollResponse struct {
	Changed   bool              `json:"changed"`
	Timestamp int64             `json:"timestamp"`
	Services  []serviceResponse `json:"services,omitempty"`
	Events    []Event           `json:"events,omitempty"`
}

// PollResult contains the result of a poll operation.
type PollResult struct {
	Services  []Service
	Events    []Event
	Timestamp time.Time
	Changed   bool
}

// Poll long-polls for service changes. Returns services, events, and timestamp.
// If no changes, returns Changed=false with the new timestamp.
func (c *Client) Poll(ctx context.Context, since time.Time) ([]Service, time.Time, error) {
	result, err := c.PollWithEvents(ctx, since)
	if err != nil {
		return nil, time.Time{}, err
	}
	return result.Services, result.Timestamp, nil
}

// PollWithEvents long-polls for service changes and returns full event details.
func (c *Client) PollWithEvents(ctx context.Context, since time.Time) (*PollResult, error) {
	url := "http://stevedore/poll"
	if !since.IsZero() {
		url += "?since=" + strconv.FormatInt(since.Unix(), 10)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to poll: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var pollResp pollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pollResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	result := &PollResult{
		Timestamp: time.Unix(pollResp.Timestamp, 0),
		Changed:   pollResp.Changed,
		Events:    pollResp.Events,
	}

	if pollResp.Changed {
		// Log events for observability
		for _, event := range pollResp.Events {
			slog.Debug("Received event from stevedore",
				"type", event.Type,
				"deployment", event.Deployment,
				"details", event.Details,
			)
		}

		// If services included in response, use them; otherwise fetch fresh
		if len(pollResp.Services) > 0 {
			result.Services = c.parseServices(pollResp.Services)
		} else {
			// Poll returned changed=true but no services payload - fetch services explicitly
			slog.Debug("Poll returned changed without services, fetching fresh service list")
			services, err := c.GetIngressServices(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch services after poll change: %w", err)
			}
			result.Services = services
		}
	}

	return result, nil
}

// parseServices converts API responses to Service structs.
func (c *Client) parseServices(responses []serviceResponse) []Service {
	var services []Service

	for _, r := range responses {
		var svc Service
		var err error

		// Try new structured format first
		if r.Ingress != nil && r.Ingress.Enabled {
			svc = Service{
				Deployment:  r.Deployment,
				Container:   r.ContainerName,
				Subdomain:   r.Ingress.Subdomain,
				Port:        r.Ingress.Port,
				Websocket:   r.Ingress.Websocket,
				HealthCheck: r.Ingress.Healthcheck,
			}
		} else if r.Labels != nil {
			// Fall back to legacy labels format
			svc, err = parseServiceFromLabels(r.Deployment, r.ContainerName, r.Labels)
			if err != nil {
				slog.Warn("Failed to parse service labels", "container", r.ContainerName, "error", err)
				continue
			}
		} else {
			slog.Debug("Skipping service without ingress config", "container", r.ContainerName)
			continue
		}

		services = append(services, svc)
	}

	return services
}

// parseServiceFromLabels extracts service config from Docker labels.
func parseServiceFromLabels(deployment, container string, labels map[string]string) (Service, error) {
	// Check if ingress is enabled
	enabled := labels["stevedore.ingress.enabled"]
	if enabled != "true" {
		return Service{}, fmt.Errorf("ingress not enabled")
	}

	// Get subdomain (required)
	subdomain := labels["stevedore.ingress.subdomain"]
	if subdomain == "" {
		return Service{}, fmt.Errorf("missing subdomain label")
	}

	// Get port (required)
	portStr := labels["stevedore.ingress.port"]
	if portStr == "" {
		return Service{}, fmt.Errorf("missing port label")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Service{}, fmt.Errorf("invalid port: %w", err)
	}

	// Get optional settings
	websocket := labels["stevedore.ingress.websocket"] == "true"
	healthCheck := labels["stevedore.ingress.healthcheck"]

	return Service{
		Deployment:  deployment,
		Container:   container,
		Subdomain:   subdomain,
		Port:        port,
		Websocket:   websocket,
		HealthCheck: healthCheck,
	}, nil
}

// HealthCheck verifies the stevedore socket is accessible.
func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://stevedore/healthz", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach stevedore socket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

// GetTarget returns the target address for proxying.
// Uses 127.0.0.1 (IPv4 loopback) since dyndns runs with host networking and can't resolve
// container names. Using explicit IPv4 avoids issues where "localhost" resolves to ::1 (IPv6)
// but the service only binds to IPv4.
// Services must expose their ports to the host (port mapping in docker-compose).
func (s *Service) GetTarget() string {
	return fmt.Sprintf("127.0.0.1:%d", s.Port)
}

// GetHealthPath returns the health check path, defaulting to /health.
func (s *Service) GetHealthPath() string {
	if s.HealthCheck == "" {
		return "/health"
	}
	return s.HealthCheck
}
