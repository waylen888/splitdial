package config

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/waylen888/splitdial/internal/logging"
	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the proxy server.
type Config struct {
	Server     ServerConfig    `yaml:"server"`
	Routes     []RouteRule     `yaml:"routes"`
	Interfaces InterfaceConfig `yaml:"interfaces"`
	Logging    LoggingConfig   `yaml:"logging"`
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	Level      string `yaml:"level"`       // debug, info, warn, error
	Format     string `yaml:"format"`      // text, json
	Output     string `yaml:"output"`      // stdout, stderr, or file path
	MaxSize    int    `yaml:"max_size"`    // max size in MB before rotation
	MaxBackups int    `yaml:"max_backups"` // max number of old log files
	MaxAge     int    `yaml:"max_age"`     // max days to retain old logs
	Compress   bool   `yaml:"compress"`    // compress rotated files
}

// ServerConfig holds server-related configuration.
type ServerConfig struct {
	SOCKSAddr string `yaml:"socks_addr"` // e.g., "127.0.0.1:1080"
	HTTPAddr  string `yaml:"http_addr"`  // e.g., "127.0.0.1:8080"
	APIAddr   string `yaml:"api_addr"`   // e.g., "127.0.0.1:8081"
}

// InterfaceConfig holds network interface configuration.
type InterfaceConfig struct {
	Cable string `yaml:"cable"` // e.g., "en0"
	WiFi  string `yaml:"wifi"`  // e.g., "en1"
}

// RouteRule defines a routing rule.
type RouteRule struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	Match     Match  `yaml:"match"`
	Interface string `yaml:"interface"` // "cable" or "wifi"
	Enabled   bool   `yaml:"enabled"`
}

// Match defines conditions for a route rule.
type Match struct {
	Domains  []string `yaml:"domains,omitempty"`  // e.g., ["*.google.com", "example.com"]
	IPs      []string `yaml:"ips,omitempty"`      // e.g., ["192.168.1.0/24"]
	Ports    []int    `yaml:"ports,omitempty"`    // e.g., [80, 443]
	Protocol string   `yaml:"protocol,omitempty"` // "tcp" or "udp"
}

// ConfigManager manages configuration with hot-reload support.
type ConfigManager struct {
	mu       sync.RWMutex
	config   *Config
	filePath string
}

// NewConfigManager creates a new configuration manager.
func NewConfigManager(filePath string) *ConfigManager {
	return &ConfigManager{
		filePath: filePath,
		config:   DefaultConfig(),
	}
}

// DefaultConfig returns default configuration.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			SOCKSAddr: "127.0.0.1:1080",
			HTTPAddr:  "127.0.0.1:8080",
			APIAddr:   "127.0.0.1:8081",
		},
		Interfaces: InterfaceConfig{
			Cable: "en0",
			WiFi:  "en1",
		},
		Logging: LoggingConfig{
			Level:      "info",
			Format:     "text",
			Output:     "stdout",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     28,
			Compress:   true,
		},
		Routes: []RouteRule{
			{
				ID:        "default",
				Name:      "Default Route",
				Match:     Match{},
				Interface: "cable",
				Enabled:   true,
			},
		},
	}
}

// Load loads configuration from file.
func (cm *ConfigManager) Load() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Use default config if file doesn't exist
			return nil
		}
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	cm.config = &cfg
	return nil
}

// Save saves configuration to file.
func (cm *ConfigManager) Save() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	data, err := yaml.Marshal(cm.config)
	if err != nil {
		return err
	}

	return os.WriteFile(cm.filePath, data, 0644)
}

// Get returns a copy of the current configuration.
func (cm *ConfigManager) Get() *Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Return a copy to avoid race conditions
	cfg := *cm.config
	return &cfg
}

// UpdateRoutes updates the routing rules.
func (cm *ConfigManager) UpdateRoutes(routes []RouteRule) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.config.Routes = routes
}

// AddRoute adds a new route.
func (cm *ConfigManager) AddRoute(route RouteRule) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.config.Routes = append(cm.config.Routes, route)
}

// RemoveRoute removes a route by ID.
func (cm *ConfigManager) RemoveRoute(id string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for i, r := range cm.config.Routes {
		if r.ID == id {
			cm.config.Routes = append(cm.config.Routes[:i], cm.config.Routes[i+1:]...)
			return true
		}
	}
	return false
}

// WatchConfig starts watching the config file for changes.
func (cm *ConfigManager) WatchConfig(onChange func(*Config)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// Watch the directory, not the file, to handle atomic writes (rename/move)
	configDir := filepath.Dir(cm.filePath)
	if err := watcher.Add(configDir); err != nil {
		watcher.Close()
		return err
	}

	go func() {
		defer watcher.Close()
		var lastReload time.Time

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Check if the event matches our config file
				if filepath.Base(event.Name) == filepath.Base(cm.filePath) {
					// Handle Write and Create (which happens on atomic rename/move)
					if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
						// Simple debounce
						if time.Since(lastReload) < 100*time.Millisecond {
							continue
						}

						logging.Info("Configuration file changed", "op", event.Op.String())

						// Slight delay to ensure write is complete
						time.Sleep(50 * time.Millisecond)

						if err := cm.Load(); err != nil {
							logging.Error("Failed to reload configuration", "error", err)
							continue
						}

						lastReload = time.Now()
						logging.Info("Configuration reloaded successfully")

						if onChange != nil {
							// Execute callback in a non-blocking way or make sure it's fast
							// Here we execute directly as it updates router/logger which is fast
							onChange(cm.Get())
						}
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logging.Error("Config watcher error", "error", err)
			}
		}
	}()

	return nil
}
