package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/waylen888/splitdial/internal/api"
	"github.com/waylen888/splitdial/internal/config"
	"github.com/waylen888/splitdial/internal/logging"
	"github.com/waylen888/splitdial/internal/network"
	"github.com/waylen888/splitdial/internal/proxy"
	"github.com/waylen888/splitdial/internal/router"
)

// findConfigFile searches for the config file in multiple locations.
func findConfigFile(specifiedPath string) string {
	// If an absolute path is specified, use it directly
	if filepath.IsAbs(specifiedPath) {
		return specifiedPath
	}

	// Get the executable's directory
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		// Try paths relative to executable
		candidatePaths := []string{
			filepath.Join(execDir, specifiedPath),
			filepath.Join(execDir, "..", specifiedPath),
			filepath.Join(execDir, "..", "..", specifiedPath),
		}
		for _, p := range candidatePaths {
			if _, err := os.Stat(p); err == nil {
				absPath, _ := filepath.Abs(p)
				return absPath
			}
		}
	}

	// Get the current working directory
	cwd, err := os.Getwd()
	if err == nil {
		// Try path relative to CWD
		cwdPath := filepath.Join(cwd, specifiedPath)
		if _, err := os.Stat(cwdPath); err == nil {
			absPath, _ := filepath.Abs(cwdPath)
			return absPath
		}
	}

	// Return the original path as fallback
	return specifiedPath
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Find and load configuration (before logging init, use fmt)
	resolvedConfigPath := findConfigFile(*configPath)
	fmt.Printf("=== Splitdial Proxy Starting ===\n")
	fmt.Printf("Config file: %s\n", resolvedConfigPath)

	configManager := config.NewConfigManager(resolvedConfigPath)
	if err := configManager.Load(); err != nil {
		fmt.Printf("⚠️  Warning: Could not load config file: %v\n", err)
		fmt.Println("   Using default configuration")
	} else {
		fmt.Println("✅ Config file loaded successfully")
	}

	cfg := configManager.Get()

	// Initialize logging
	logCfg := &logging.Config{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
		Output: cfg.Logging.Output,
	}
	if cfg.Logging.Output != "stdout" && cfg.Logging.Output != "stderr" {
		logCfg.FileConfig = &logging.FileConfig{
			MaxSize:    cfg.Logging.MaxSize,
			MaxBackups: cfg.Logging.MaxBackups,
			MaxAge:     cfg.Logging.MaxAge,
			Compress:   cfg.Logging.Compress,
		}
	}
	if err := logging.Init(logCfg); err != nil {
		fmt.Printf("Failed to initialize logging: %v\n", err)
		os.Exit(1)
	}

	// From now on, use slog
	logging.Info("Logging initialized", "level", cfg.Logging.Level, "format", cfg.Logging.Format, "output", cfg.Logging.Output)

	// Resolve interface specifications to device names
	resolver := network.NewInterfaceResolver()

	cableDevice, err := resolver.ResolveDeviceName(cfg.Interfaces.Cable)
	if err != nil {
		logging.Warn("Could not resolve cable interface", "spec", cfg.Interfaces.Cable, "error", err)
		// Try auto-detection as fallback
		if detected, _, detectErr := network.DetectInterfaces(); detectErr == nil && detected != "" {
			cableDevice = detected
			logging.Info("Auto-detected cable interface", "device", cableDevice)
		}
	}

	wifiDevice, err := resolver.ResolveDeviceName(cfg.Interfaces.WiFi)
	if err != nil {
		logging.Warn("Could not resolve wifi interface", "spec", cfg.Interfaces.WiFi, "error", err)
		// Try auto-detection as fallback
		if _, detected, detectErr := network.DetectInterfaces(); detectErr == nil && detected != "" {
			wifiDevice = detected
			logging.Info("Auto-detected wifi interface", "device", wifiDevice)
		}
	}

	// Print interface configuration
	logging.Info("Interface configuration",
		"cable_spec", cfg.Interfaces.Cable,
		"cable_device", cableDevice,
		"wifi_spec", cfg.Interfaces.WiFi,
		"wifi_device", wifiDevice,
	)

	// Print interface IP addresses for verification
	im := network.NewInterfaceManager(cableDevice, wifiDevice)
	if cableAddr, err := im.GetLocalAddr("cable"); err == nil {
		logging.Info("Cable interface ready", "device", cableDevice, "ip", cableAddr.IP.String())
	} else {
		logging.Warn("Cable interface error", "device", cableDevice, "error", err)
	}
	if wifiAddr, err := im.GetLocalAddr("wifi"); err == nil {
		logging.Info("Wi-Fi interface ready", "device", wifiDevice, "ip", wifiAddr.IP.String())
	} else {
		logging.Warn("Wi-Fi interface error", "device", wifiDevice, "error", err)
	}

	// Print routing rules
	logging.Info("Routing rules loaded", "count", len(cfg.Routes))
	for _, rule := range cfg.Routes {
		logging.Debug("Route rule",
			"id", rule.ID,
			"name", rule.Name,
			"interface", rule.Interface,
			"enabled", rule.Enabled,
		)
	}

	// Initialize components
	interfaceManager := network.NewInterfaceManager(cableDevice, wifiDevice)
	interfaceDialer := network.NewInterfaceDialer(interfaceManager, 30*time.Second)
	routerEngine := router.NewRouter(cfg.Routes)

	// Start watching config for changes
	if err := configManager.WatchConfig(func(newCfg *config.Config) {
		logging.Info("Applying new configuration...")

		// Update router rules
		routerEngine.UpdateRules(newCfg.Routes)

		// Update logging level
		logging.SetLevel(newCfg.Logging.Level)

		logging.Info("Configuration applied",
			"rules_count", len(newCfg.Routes),
			"log_level", newCfg.Logging.Level,
		)
	}); err != nil {
		logging.Warn("Failed to start config watcher", "error", err)
	}

	// Create proxy servers
	socks5Server := proxy.NewSOCKS5Server(cfg.Server.SOCKSAddr, routerEngine, interfaceDialer)
	httpProxy := proxy.NewHTTPProxyServer(cfg.Server.HTTPAddr, routerEngine, interfaceDialer)
	apiServer := api.NewServer(cfg.Server.APIAddr, configManager, interfaceManager, routerEngine)

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logging.Info("Shutting down...")
		cancel()
	}()

	// Start servers
	errChan := make(chan error, 3)

	go func() {
		if err := socks5Server.Start(ctx); err != nil {
			errChan <- err
		}
	}()

	go func() {
		if err := httpProxy.Start(ctx); err != nil {
			errChan <- err
		}
	}()

	go func() {
		if err := apiServer.Start(); err != nil {
			errChan <- err
		}
	}()

	logging.Info("Proxy server started successfully!",
		"socks5", cfg.Server.SOCKSAddr,
		"http", cfg.Server.HTTPAddr,
		"api", cfg.Server.APIAddr,
	)

	// Wait for error or context cancellation
	select {
	case err := <-errChan:
		logging.Error("Server error", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		logging.Info("Server stopped")
	}
}
