package discovery

import "testing"

func TestServicesEqual_OrderIndependent(t *testing.T) {
	a := []Service{
		{Subdomain: "app", Port: 8080, Websocket: false, HealthCheck: "/health"},
		{Subdomain: "api", Port: 9000, Websocket: true, HealthCheck: "/status"},
	}
	b := []Service{
		{Subdomain: "api", Port: 9000, Websocket: true, HealthCheck: "/status"},
		{Subdomain: "app", Port: 8080, Websocket: false, HealthCheck: "/health"},
	}

	if !ServicesEqual(a, b) {
		t.Fatal("expected services to be equal regardless of order")
	}
}

func TestServicesEqual_HealthPathDefault(t *testing.T) {
	a := []Service{
		{Subdomain: "app", Port: 8080, Websocket: false, HealthCheck: ""},
	}
	b := []Service{
		{Subdomain: "app", Port: 8080, Websocket: false, HealthCheck: "/health"},
	}

	if !ServicesEqual(a, b) {
		t.Fatal("expected default health path to be treated as /health")
	}
}

func TestServicesEqual_DifferentFields(t *testing.T) {
	tests := []struct {
		name string
		a    []Service
		b    []Service
	}{
		{
			name: "different port",
			a:    []Service{{Subdomain: "app", Port: 8080}},
			b:    []Service{{Subdomain: "app", Port: 9090}},
		},
		{
			name: "different websocket",
			a:    []Service{{Subdomain: "app", Port: 8080, Websocket: false}},
			b:    []Service{{Subdomain: "app", Port: 8080, Websocket: true}},
		},
		{
			name: "different health path",
			a:    []Service{{Subdomain: "app", Port: 8080, HealthCheck: "/health"}},
			b:    []Service{{Subdomain: "app", Port: 8080, HealthCheck: "/healthz"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ServicesEqual(tt.a, tt.b) {
				t.Fatalf("expected services to differ for case: %s", tt.name)
			}
		})
	}
}

func TestServicesEqual_DuplicateCounts(t *testing.T) {
	a := []Service{
		{Subdomain: "app", Port: 8080},
		{Subdomain: "app", Port: 8080},
	}
	b := []Service{
		{Subdomain: "app", Port: 8080},
	}

	if ServicesEqual(a, b) {
		t.Fatal("expected services with different counts to be unequal")
	}
}
