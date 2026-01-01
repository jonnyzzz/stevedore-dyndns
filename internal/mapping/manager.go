package mapping

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// subdomainRegex validates DNS label format
// Must start and end with alphanumeric, can contain hyphens, max 63 chars
var subdomainRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// Mapping represents a subdomain to service mapping
type Mapping struct {
	Subdomain      string         `yaml:"subdomain"`
	Target         string         `yaml:"target,omitempty"`          // Direct host:port target
	ComposeProject string         `yaml:"compose_project,omitempty"` // Docker Compose project name
	ComposeService string         `yaml:"compose_service,omitempty"` // Docker Compose service name
	Container      string         `yaml:"container,omitempty"`       // Docker container name
	Port           int            `yaml:"port,omitempty"`            // Port for container/compose service
	Options        MappingOptions `yaml:"options,omitempty"`
}

// MappingOptions contains optional configuration for a mapping
type MappingOptions struct {
	Websocket      bool   `yaml:"websocket,omitempty"`
	BufferRequests bool   `yaml:"buffer_requests,omitempty"`
	HealthPath     string `yaml:"health_path,omitempty"`
}

// MappingsFile represents the structure of the mappings.yaml file
type MappingsFile struct {
	Mappings []Mapping `yaml:"mappings"`
}

// Manager handles loading and watching the mappings file
type Manager struct {
	filePath string
	mappings []Mapping
	mu       sync.RWMutex
}

// New creates a new mapping manager
func New(filePath string) *Manager {
	return &Manager{
		filePath: filePath,
		mappings: []Mapping{},
	}
}

// Load reads the mappings from the file
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("Mappings file not found, using empty mappings", "path", m.filePath)
			m.mappings = []Mapping{}
			return nil
		}
		return fmt.Errorf("failed to read mappings file: %w", err)
	}

	var file MappingsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("failed to parse mappings file: %w", err)
	}

	// Validate and resolve mappings, only keeping valid ones
	validMappings := make([]Mapping, 0, len(file.Mappings))
	for i := range file.Mappings {
		if err := m.validateMapping(&file.Mappings[i]); err != nil {
			slog.Warn("Skipping invalid mapping", "subdomain", file.Mappings[i].Subdomain, "error", err)
			continue
		}
		if err := m.resolveMapping(&file.Mappings[i]); err != nil {
			slog.Warn("Skipping unresolved mapping", "subdomain", file.Mappings[i].Subdomain, "error", err)
			continue
		}
		validMappings = append(validMappings, file.Mappings[i])
	}

	m.mappings = validMappings
	slog.Info("Loaded mappings", "valid", len(validMappings), "total", len(file.Mappings))
	return nil
}

// Get returns all current mappings
func (m *Manager) Get() []Mapping {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Mapping, len(m.mappings))
	copy(result, m.mappings)
	return result
}

// Watch monitors the mappings file for changes
func (m *Manager) Watch(ctx context.Context, onChange func()) {
	// Note: Initial load is now done by caller before Watch() is called
	// This prevents race conditions between loading and watching

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("Failed to create file watcher", "error", err)
		return
	}
	defer watcher.Close()

	// Watch the directory containing the mappings file, not the file itself
	// This allows us to detect when the file is created if it doesn't exist yet
	dir := filepath.Dir(m.filePath)
	filename := filepath.Base(m.filePath)

	if err := watcher.Add(dir); err != nil {
		slog.Error("Failed to watch mappings directory", "path", dir, "error", err)
		return
	}
	slog.Info("Watching for mappings file changes", "directory", dir, "filename", filename)

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Only react to events for our specific file
			if filepath.Base(event.Name) != filename {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				slog.Info("Mappings file changed", "event", event.Op, "file", event.Name)
				if err := m.Load(); err != nil {
					slog.Error("Failed to reload mappings", "error", err)
				} else if onChange != nil {
					onChange()
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("File watcher error", "error", err)
		}
	}
}

func (m *Manager) validateMapping(mapping *Mapping) error {
	if mapping.Subdomain == "" {
		return fmt.Errorf("subdomain is required")
	}

	// Validate subdomain format (DNS label)
	if !subdomainRegex.MatchString(mapping.Subdomain) {
		return fmt.Errorf("subdomain %q is invalid: must be alphanumeric with optional hyphens, 1-63 chars", mapping.Subdomain)
	}

	// Must have at least one target specification
	hasTarget := mapping.Target != ""
	hasCompose := mapping.ComposeProject != "" && mapping.ComposeService != ""
	hasContainer := mapping.Container != ""

	if !hasTarget && !hasCompose && !hasContainer {
		return fmt.Errorf("must specify target, compose_project+compose_service, or container")
	}

	// Validate port if specified
	if mapping.Port != 0 && (mapping.Port < 1 || mapping.Port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535, got %d", mapping.Port)
	}

	return nil
}

func (m *Manager) resolveMapping(mapping *Mapping) error {
	// If target is already set, nothing to resolve
	if mapping.Target != "" {
		return nil
	}

	// Resolve compose service to target
	if mapping.ComposeProject != "" && mapping.ComposeService != "" {
		port := mapping.Port
		if port == 0 {
			port = 80 // Default port
		}
		// Docker Compose creates containers with names like: project-service-1
		// or project_service_1 depending on version
		containerName := fmt.Sprintf("%s-%s-1", mapping.ComposeProject, mapping.ComposeService)
		mapping.Target = fmt.Sprintf("%s:%d", containerName, port)
		return nil
	}

	// Resolve container name to target
	if mapping.Container != "" {
		port := mapping.Port
		if port == 0 {
			port = 80
		}
		mapping.Target = fmt.Sprintf("%s:%d", mapping.Container, port)
		return nil
	}

	return fmt.Errorf("could not resolve target")
}

// GetTarget returns the resolved target for a mapping
func (mapping *Mapping) GetTarget() string {
	return mapping.Target
}

// GetHealthPath returns the health check path or default
func (mapping *Mapping) GetHealthPath() string {
	if mapping.Options.HealthPath != "" {
		return mapping.Options.HealthPath
	}
	return "/health"
}
