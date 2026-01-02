package discovery

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseServiceFromLabels(t *testing.T) {
	tests := []struct {
		name        string
		deployment  string
		container   string
		labels      map[string]string
		wantService Service
		wantErr     bool
	}{
		{
			name:       "valid service",
			deployment: "myapp",
			container:  "stevedore-myapp-web-1",
			labels: map[string]string{
				"stevedore.ingress.enabled":   "true",
				"stevedore.ingress.subdomain": "myapp",
				"stevedore.ingress.port":      "3000",
			},
			wantService: Service{
				Deployment: "myapp",
				Container:  "stevedore-myapp-web-1",
				Subdomain:  "myapp",
				Port:       3000,
			},
			wantErr: false,
		},
		{
			name:       "with websocket",
			deployment: "chat",
			container:  "stevedore-chat-app-1",
			labels: map[string]string{
				"stevedore.ingress.enabled":   "true",
				"stevedore.ingress.subdomain": "chat",
				"stevedore.ingress.port":      "8080",
				"stevedore.ingress.websocket": "true",
			},
			wantService: Service{
				Deployment: "chat",
				Container:  "stevedore-chat-app-1",
				Subdomain:  "chat",
				Port:       8080,
				Websocket:  true,
			},
			wantErr: false,
		},
		{
			name:       "with healthcheck",
			deployment: "api",
			container:  "stevedore-api-server-1",
			labels: map[string]string{
				"stevedore.ingress.enabled":     "true",
				"stevedore.ingress.subdomain":   "api",
				"stevedore.ingress.port":        "9000",
				"stevedore.ingress.healthcheck": "/healthz",
			},
			wantService: Service{
				Deployment:  "api",
				Container:   "stevedore-api-server-1",
				Subdomain:   "api",
				Port:        9000,
				HealthCheck: "/healthz",
			},
			wantErr: false,
		},
		{
			name:       "ingress not enabled",
			deployment: "myapp",
			container:  "stevedore-myapp-web-1",
			labels: map[string]string{
				"stevedore.ingress.enabled":   "false",
				"stevedore.ingress.subdomain": "myapp",
				"stevedore.ingress.port":      "3000",
			},
			wantErr: true,
		},
		{
			name:       "missing subdomain",
			deployment: "myapp",
			container:  "stevedore-myapp-web-1",
			labels: map[string]string{
				"stevedore.ingress.enabled": "true",
				"stevedore.ingress.port":    "3000",
			},
			wantErr: true,
		},
		{
			name:       "missing port",
			deployment: "myapp",
			container:  "stevedore-myapp-web-1",
			labels: map[string]string{
				"stevedore.ingress.enabled":   "true",
				"stevedore.ingress.subdomain": "myapp",
			},
			wantErr: true,
		},
		{
			name:       "invalid port",
			deployment: "myapp",
			container:  "stevedore-myapp-web-1",
			labels: map[string]string{
				"stevedore.ingress.enabled":   "true",
				"stevedore.ingress.subdomain": "myapp",
				"stevedore.ingress.port":      "not-a-number",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := parseServiceFromLabels(tt.deployment, tt.container, tt.labels)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseServiceFromLabels() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("parseServiceFromLabels() unexpected error: %v", err)
				return
			}

			if svc.Deployment != tt.wantService.Deployment {
				t.Errorf("Deployment = %q, want %q", svc.Deployment, tt.wantService.Deployment)
			}
			if svc.Container != tt.wantService.Container {
				t.Errorf("Container = %q, want %q", svc.Container, tt.wantService.Container)
			}
			if svc.Subdomain != tt.wantService.Subdomain {
				t.Errorf("Subdomain = %q, want %q", svc.Subdomain, tt.wantService.Subdomain)
			}
			if svc.Port != tt.wantService.Port {
				t.Errorf("Port = %d, want %d", svc.Port, tt.wantService.Port)
			}
			if svc.Websocket != tt.wantService.Websocket {
				t.Errorf("Websocket = %v, want %v", svc.Websocket, tt.wantService.Websocket)
			}
			if svc.HealthCheck != tt.wantService.HealthCheck {
				t.Errorf("HealthCheck = %q, want %q", svc.HealthCheck, tt.wantService.HealthCheck)
			}
		})
	}
}

func TestService_GetTarget(t *testing.T) {
	svc := Service{
		Container: "stevedore-myapp-web-1",
		Port:      3000,
	}

	got := svc.GetTarget()
	want := "stevedore-myapp-web-1:3000"

	if got != want {
		t.Errorf("GetTarget() = %q, want %q", got, want)
	}
}

func TestService_GetHealthPath(t *testing.T) {
	tests := []struct {
		name        string
		healthCheck string
		want        string
	}{
		{"default", "", "/health"},
		{"custom", "/healthz", "/healthz"},
		{"api path", "/api/health", "/api/health"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := Service{HealthCheck: tt.healthCheck}
			got := svc.GetHealthPath()
			if got != tt.want {
				t.Errorf("GetHealthPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestClient_MockServer tests the client against a mock stevedore socket.
func TestClient_MockServer(t *testing.T) {
	// Create temp socket
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "query.sock")

	// Create mock server
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create socket: %v", err)
	}
	defer listener.Close()

	// Mock server handler
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/services", func(w http.ResponseWriter, r *http.Request) {
		// Check auth
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		services := []serviceResponse{
			{
				Deployment: "myapp",
				Container:  "stevedore-myapp-web-1",
				Labels: map[string]string{
					"stevedore.ingress.enabled":   "true",
					"stevedore.ingress.subdomain": "myapp",
					"stevedore.ingress.port":      "3000",
				},
			},
			{
				Deployment: "api",
				Container:  "stevedore-api-server-1",
				Labels: map[string]string{
					"stevedore.ingress.enabled":     "true",
					"stevedore.ingress.subdomain":   "api",
					"stevedore.ingress.port":        "8080",
					"stevedore.ingress.websocket":   "true",
					"stevedore.ingress.healthcheck": "/healthz",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(services)
	})

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	// Create client
	client := New(Config{
		SocketPath: socketPath,
		Token:      "test-token",
	})

	t.Run("HealthCheck", func(t *testing.T) {
		err := client.HealthCheck(context.Background())
		if err != nil {
			t.Errorf("HealthCheck() unexpected error: %v", err)
		}
	})

	t.Run("GetIngressServices", func(t *testing.T) {
		services, err := client.GetIngressServices(context.Background())
		if err != nil {
			t.Fatalf("GetIngressServices() unexpected error: %v", err)
		}

		if len(services) != 2 {
			t.Errorf("GetIngressServices() returned %d services, want 2", len(services))
		}

		// Check first service
		if services[0].Subdomain != "myapp" {
			t.Errorf("services[0].Subdomain = %q, want %q", services[0].Subdomain, "myapp")
		}
		if services[0].Port != 3000 {
			t.Errorf("services[0].Port = %d, want %d", services[0].Port, 3000)
		}

		// Check second service
		if services[1].Subdomain != "api" {
			t.Errorf("services[1].Subdomain = %q, want %q", services[1].Subdomain, "api")
		}
		if !services[1].Websocket {
			t.Error("services[1].Websocket should be true")
		}
	})

	t.Run("UnauthorizedRequest", func(t *testing.T) {
		// Create client with wrong token
		badClient := New(Config{
			SocketPath: socketPath,
			Token:      "wrong-token",
		})

		_, err := badClient.GetIngressServices(context.Background())
		if err == nil {
			t.Error("GetIngressServices() with bad token should return error")
		}
	})
}

func TestNew(t *testing.T) {
	cfg := Config{
		SocketPath: "/var/run/stevedore/query.sock",
		Token:      "test-token",
	}

	client := New(cfg)

	if client == nil {
		t.Fatal("New() returned nil")
	}
	if client.socketPath != cfg.SocketPath {
		t.Errorf("socketPath = %q, want %q", client.socketPath, cfg.SocketPath)
	}
	if client.token != cfg.Token {
		t.Errorf("token = %q, want %q", client.token, cfg.Token)
	}
	if client.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

// TestClient_SocketNotExists tests behavior when socket doesn't exist.
func TestClient_SocketNotExists(t *testing.T) {
	client := New(Config{
		SocketPath: "/nonexistent/socket.sock",
		Token:      "test-token",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.GetIngressServices(ctx)
	if err == nil {
		t.Error("GetIngressServices() with nonexistent socket should return error")
	}
}

// Ensure socket file is cleaned up in tests
func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
