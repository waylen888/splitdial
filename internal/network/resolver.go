package network

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"

	"github.com/waylen888/splitdial/internal/config"
	"github.com/waylen888/splitdial/internal/logging"
)

// InterfaceResolver resolves interface specifications to device names.
type InterfaceResolver struct {
	// Cache of hardware port to device name mappings
	portToDevice map[string]string
}

// NewInterfaceResolver creates a new interface resolver.
func NewInterfaceResolver() *InterfaceResolver {
	return &InterfaceResolver{
		portToDevice: make(map[string]string),
	}
}

// ResolveDeviceName resolves an InterfaceSpec to an actual device name.
// If Device is specified, it returns that directly.
// If HardwarePort is specified, it queries macOS networksetup to find the device.
func (r *InterfaceResolver) ResolveDeviceName(spec config.InterfaceSpec) (string, error) {
	// If device is directly specified, use it
	if spec.Device != "" {
		return spec.Device, nil
	}

	// If hardware port is specified, resolve it
	if spec.HardwarePort != "" {
		return r.resolveHardwarePort(spec.HardwarePort)
	}

	return "", fmt.Errorf("interface spec has neither device nor hardware_port specified")
}

// resolveHardwarePort resolves a hardware port name to its device name.
func (r *InterfaceResolver) resolveHardwarePort(portName string) (string, error) {
	// Check cache first
	if device, ok := r.portToDevice[portName]; ok {
		return device, nil
	}

	// Refresh cache
	if err := r.refreshPortMappings(); err != nil {
		return "", err
	}

	// Check again after refresh
	if device, ok := r.portToDevice[portName]; ok {
		return device, nil
	}

	return "", fmt.Errorf("hardware port %q not found", portName)
}

// refreshPortMappings queries networksetup to get current port-to-device mappings.
func (r *InterfaceResolver) refreshPortMappings() error {
	cmd := exec.Command("networksetup", "-listallhardwareports")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to execute networksetup: %w", err)
	}

	r.portToDevice = parseNetworkSetupOutput(string(output))

	logging.Debug("Refreshed hardware port mappings",
		"mappings", r.portToDevice,
	)

	return nil
}

// parseNetworkSetupOutput parses the output of 'networksetup -listallhardwareports'
// and returns a map of hardware port names to device names.
//
// Example output format:
//
//	Hardware Port: Wi-Fi
//	Device: en0
//	Ethernet Address: bc:d0:74:1e:5b:f1
//
//	Hardware Port: USB 10/100/1000 LAN
//	Device: en7
//	Ethernet Address: 00:14:3d:28:0b:ad
func parseNetworkSetupOutput(output string) map[string]string {
	result := make(map[string]string)

	scanner := bufio.NewScanner(strings.NewReader(output))
	var currentPort string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "Hardware Port:") {
			currentPort = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
		} else if strings.HasPrefix(line, "Device:") && currentPort != "" {
			device := strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			if device != "" {
				result[currentPort] = device
			}
		}
	}

	return result
}

// GetAllPorts returns all available hardware ports and their device mappings.
func (r *InterfaceResolver) GetAllPorts() (map[string]string, error) {
	if err := r.refreshPortMappings(); err != nil {
		return nil, err
	}
	// Return a copy
	result := make(map[string]string, len(r.portToDevice))
	for k, v := range r.portToDevice {
		result[k] = v
	}
	return result, nil
}
