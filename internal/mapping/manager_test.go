package mapping

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManager_Load_FileNotExists(t *testing.T) {
	mgr := New("/nonexistent/path/mappings.yaml")
	err := mgr.Load()
	if err != nil {
		t.Errorf("Load() with nonexistent file should not error, got: %v", err)
	}

	mappings := mgr.Get()
	if len(mappings) != 0 {
		t.Errorf("Load() with nonexistent file should return empty mappings, got %d", len(mappings))
	}
}

func TestManager_Load_ValidFile(t *testing.T) {
	// Create temp file with valid mappings
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mappings.yaml")

	content := `
mappings:
  - subdomain: app1
    target: "192.168.1.100:8080"
  - subdomain: app2
    compose_project: stevedore-myapp
    compose_service: web
    port: 3000
  - subdomain: api
    container: my-container
    port: 8000
`
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	mgr := New(tmpFile)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	mappings := mgr.Get()
	if len(mappings) != 3 {
		t.Errorf("Load() got %d mappings, want 3", len(mappings))
	}

	// Check first mapping (direct target)
	if mappings[0].Subdomain != "app1" {
		t.Errorf("mappings[0].Subdomain = %q, want %q", mappings[0].Subdomain, "app1")
	}
	if mappings[0].Target != "192.168.1.100:8080" {
		t.Errorf("mappings[0].Target = %q, want %q", mappings[0].Target, "192.168.1.100:8080")
	}

	// Check second mapping (compose - should be resolved)
	if mappings[1].Subdomain != "app2" {
		t.Errorf("mappings[1].Subdomain = %q, want %q", mappings[1].Subdomain, "app2")
	}
	if mappings[1].Target != "stevedore-myapp-web-1:3000" {
		t.Errorf("mappings[1].Target = %q, want %q", mappings[1].Target, "stevedore-myapp-web-1:3000")
	}

	// Check third mapping (container - should be resolved)
	if mappings[2].Subdomain != "api" {
		t.Errorf("mappings[2].Subdomain = %q, want %q", mappings[2].Subdomain, "api")
	}
	if mappings[2].Target != "my-container:8000" {
		t.Errorf("mappings[2].Target = %q, want %q", mappings[2].Target, "my-container:8000")
	}
}

func TestManager_Load_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mappings.yaml")

	content := `invalid: yaml: content: [[[`
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	mgr := New(tmpFile)
	err := mgr.Load()
	if err == nil {
		t.Error("Load() with invalid YAML should return error")
	}
}

func TestManager_Load_SkipsInvalidMappings(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mappings.yaml")

	content := `
mappings:
  - subdomain: valid-app
    target: "192.168.1.100:8080"
  - subdomain: ""
    target: "should-be-skipped"
  - subdomain: -invalid-start
    target: "192.168.1.101:8080"
  - subdomain: no-target
  - subdomain: another-valid
    target: "192.168.1.102:8080"
`
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	mgr := New(tmpFile)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	mappings := mgr.Get()
	// Should only have valid-app and another-valid (2 valid, 3 invalid skipped)
	if len(mappings) != 2 {
		t.Errorf("Load() got %d valid mappings, want 2", len(mappings))
	}

	if len(mappings) > 0 && mappings[0].Subdomain != "valid-app" {
		t.Errorf("mappings[0].Subdomain = %q, want %q", mappings[0].Subdomain, "valid-app")
	}
	if len(mappings) > 1 && mappings[1].Subdomain != "another-valid" {
		t.Errorf("mappings[1].Subdomain = %q, want %q", mappings[1].Subdomain, "another-valid")
	}
}

func TestManager_Load_WithOptions(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mappings.yaml")

	content := `
mappings:
  - subdomain: streaming
    target: "media:8096"
    options:
      websocket: true
      buffer_requests: false
      health_path: /api/health
`
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	mgr := New(tmpFile)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	mappings := mgr.Get()
	if len(mappings) != 1 {
		t.Fatalf("Load() got %d mappings, want 1", len(mappings))
	}

	m := mappings[0]
	if !m.Options.Websocket {
		t.Error("Options.Websocket should be true")
	}
	if m.Options.BufferRequests {
		t.Error("Options.BufferRequests should be false")
	}
	if m.Options.HealthPath != "/api/health" {
		t.Errorf("Options.HealthPath = %q, want %q", m.Options.HealthPath, "/api/health")
	}
}

func TestManager_Watch(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mappings.yaml")

	// Create initial file
	initialContent := `
mappings:
  - subdomain: app1
    target: "192.168.1.100:8080"
`
	if err := os.WriteFile(tmpFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	mgr := New(tmpFile)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changed := make(chan bool, 1)
	go mgr.Watch(ctx, func() {
		changed <- true
	})

	// Give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// Update file
	updatedContent := `
mappings:
  - subdomain: app1
    target: "192.168.1.100:8080"
  - subdomain: app2
    target: "192.168.1.101:8080"
`
	if err := os.WriteFile(tmpFile, []byte(updatedContent), 0644); err != nil {
		t.Fatalf("Failed to update test file: %v", err)
	}

	// Wait for change notification
	select {
	case <-changed:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Watch() did not detect file change")
	}

	// Verify mappings were updated
	mappings := mgr.Get()
	if len(mappings) != 2 {
		t.Errorf("After update, got %d mappings, want 2", len(mappings))
	}
}

func TestManager_Watch_NewFileCreated(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mappings.yaml")

	// Don't create file initially
	mgr := New(tmpFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changed := make(chan bool, 1)
	go mgr.Watch(ctx, func() {
		changed <- true
	})

	// Give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// Now create the file
	content := `
mappings:
  - subdomain: newapp
    target: "192.168.1.100:8080"
`
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Wait for change notification
	select {
	case <-changed:
		// Success - file creation was detected
	case <-time.After(2 * time.Second):
		t.Error("Watch() did not detect new file creation")
	}

	// Verify mappings were loaded
	mappings := mgr.Get()
	if len(mappings) != 1 {
		t.Errorf("After file creation, got %d mappings, want 1", len(mappings))
	}
}

func TestValidateMapping(t *testing.T) {
	mgr := New("")

	tests := []struct {
		name    string
		mapping Mapping
		wantErr bool
	}{
		{
			name:    "empty subdomain",
			mapping: Mapping{Subdomain: "", Target: "host:80"},
			wantErr: true,
		},
		{
			name:    "valid subdomain with target",
			mapping: Mapping{Subdomain: "app", Target: "host:80"},
			wantErr: false,
		},
		{
			name:    "valid subdomain with compose",
			mapping: Mapping{Subdomain: "app", ComposeProject: "proj", ComposeService: "svc"},
			wantErr: false,
		},
		{
			name:    "valid subdomain with container",
			mapping: Mapping{Subdomain: "app", Container: "container"},
			wantErr: false,
		},
		{
			name:    "no target specification",
			mapping: Mapping{Subdomain: "app"},
			wantErr: true,
		},
		{
			name:    "invalid subdomain - starts with hyphen",
			mapping: Mapping{Subdomain: "-invalid", Target: "host:80"},
			wantErr: true,
		},
		{
			name:    "invalid subdomain - ends with hyphen",
			mapping: Mapping{Subdomain: "invalid-", Target: "host:80"},
			wantErr: true,
		},
		{
			name:    "subdomain with consecutive hyphens (valid per DNS spec)",
			mapping: Mapping{Subdomain: "in--valid", Target: "host:80"},
			wantErr: false, // DNS labels allow consecutive hyphens (e.g., punycode xn--)
		},
		{
			name:    "valid subdomain - single char",
			mapping: Mapping{Subdomain: "a", Target: "host:80"},
			wantErr: false,
		},
		{
			name:    "valid subdomain - with hyphens",
			mapping: Mapping{Subdomain: "my-cool-app", Target: "host:80"},
			wantErr: false,
		},
		{
			name:    "valid subdomain - alphanumeric",
			mapping: Mapping{Subdomain: "app123", Target: "host:80"},
			wantErr: false,
		},
		{
			name:    "invalid port - zero",
			mapping: Mapping{Subdomain: "app", Container: "c", Port: 0},
			wantErr: false, // Port 0 uses default
		},
		{
			name:    "invalid port - negative",
			mapping: Mapping{Subdomain: "app", Container: "c", Port: -1},
			wantErr: true,
		},
		{
			name:    "invalid port - too high",
			mapping: Mapping{Subdomain: "app", Container: "c", Port: 65536},
			wantErr: true,
		},
		{
			name:    "valid port",
			mapping: Mapping{Subdomain: "app", Container: "c", Port: 8080},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mgr.validateMapping(&tt.mapping)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMapping() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolveMapping(t *testing.T) {
	mgr := New("")

	tests := []struct {
		name       string
		mapping    Mapping
		wantTarget string
	}{
		{
			name:       "target already set",
			mapping:    Mapping{Subdomain: "app", Target: "already:set"},
			wantTarget: "already:set",
		},
		{
			name:       "compose service - default port",
			mapping:    Mapping{Subdomain: "app", ComposeProject: "proj", ComposeService: "web"},
			wantTarget: "proj-web-1:80",
		},
		{
			name:       "compose service - custom port",
			mapping:    Mapping{Subdomain: "app", ComposeProject: "proj", ComposeService: "api", Port: 3000},
			wantTarget: "proj-api-1:3000",
		},
		{
			name:       "container - default port",
			mapping:    Mapping{Subdomain: "app", Container: "mycontainer"},
			wantTarget: "mycontainer:80",
		},
		{
			name:       "container - custom port",
			mapping:    Mapping{Subdomain: "app", Container: "mycontainer", Port: 9000},
			wantTarget: "mycontainer:9000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mgr.resolveMapping(&tt.mapping)
			if err != nil {
				t.Errorf("resolveMapping() unexpected error: %v", err)
				return
			}
			if tt.mapping.Target != tt.wantTarget {
				t.Errorf("resolveMapping() Target = %q, want %q", tt.mapping.Target, tt.wantTarget)
			}
		})
	}
}

func TestMapping_GetTarget(t *testing.T) {
	m := &Mapping{Target: "host:8080"}
	if got := m.GetTarget(); got != "host:8080" {
		t.Errorf("GetTarget() = %q, want %q", got, "host:8080")
	}
}

func TestMapping_GetHealthPath(t *testing.T) {
	tests := []struct {
		name    string
		mapping Mapping
		want    string
	}{
		{
			name:    "default health path",
			mapping: Mapping{},
			want:    "/health",
		},
		{
			name:    "custom health path",
			mapping: Mapping{Options: MappingOptions{HealthPath: "/api/healthz"}},
			want:    "/api/healthz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mapping.GetHealthPath(); got != tt.want {
				t.Errorf("GetHealthPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManager_Get_ThreadSafety(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mappings.yaml")

	content := `
mappings:
  - subdomain: app
    target: "host:80"
`
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	mgr := New(tmpFile)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// Run concurrent reads and loads
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = mgr.Get()
			}
			done <- true
		}()
		go func() {
			for j := 0; j < 100; j++ {
				_ = mgr.Load()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}
}
