package network

import (
	"fmt"
	"net"
	"strings"
)

// Interface represents a network interface with its addresses.
type Interface struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"` // "cable", "wifi", "other"
	IPv4Addrs  []string `json:"ipv4_addrs"`
	IPv6Addrs  []string `json:"ipv6_addrs"`
	MACAddress string   `json:"mac_address"`
	IsUp       bool     `json:"is_up"`
	MTU        int      `json:"mtu"`
}

// InterfaceManager handles network interface detection and management.
type InterfaceManager struct {
	cableInterfaceName string
	wifiInterfaceName  string
}

// NewInterfaceManager creates a new interface manager.
func NewInterfaceManager(cableName, wifiName string) *InterfaceManager {
	return &InterfaceManager{
		cableInterfaceName: cableName,
		wifiInterfaceName:  wifiName,
	}
}

// ListInterfaces returns all available network interfaces.
func (im *InterfaceManager) ListInterfaces() ([]Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list interfaces: %w", err)
	}

	var result []Interface
	for _, iface := range ifaces {
		// Skip loopback and inactive interfaces
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		netIface := Interface{
			Name:       iface.Name,
			Type:       im.detectInterfaceType(iface.Name),
			MACAddress: iface.HardwareAddr.String(),
			IsUp:       iface.Flags&net.FlagUp != 0,
			MTU:        iface.MTU,
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP
			if ip.To4() != nil {
				netIface.IPv4Addrs = append(netIface.IPv4Addrs, ip.String())
			} else if ip.To16() != nil {
				netIface.IPv6Addrs = append(netIface.IPv6Addrs, ip.String())
			}
		}

		// Only include interfaces with at least one IP address
		if len(netIface.IPv4Addrs) > 0 || len(netIface.IPv6Addrs) > 0 {
			result = append(result, netIface)
		}
	}

	return result, nil
}

// GetInterfaceByType returns the interface by type ("cable" or "wifi").
func (im *InterfaceManager) GetInterfaceByType(interfaceType string) (*Interface, error) {
	ifaces, err := im.ListInterfaces()
	if err != nil {
		return nil, err
	}

	var targetName string
	switch interfaceType {
	case "cable":
		targetName = im.cableInterfaceName
	case "wifi":
		targetName = im.wifiInterfaceName
	default:
		return nil, fmt.Errorf("unknown interface type: %s", interfaceType)
	}

	for _, iface := range ifaces {
		if iface.Name == targetName {
			return &iface, nil
		}
	}

	return nil, fmt.Errorf("interface %s not found", targetName)
}

// GetLocalAddr returns the local IPv4 address to bind for a given interface type.
func (im *InterfaceManager) GetLocalAddr(interfaceType string) (*net.TCPAddr, error) {
	iface, err := im.GetInterfaceByType(interfaceType)
	if err != nil {
		return nil, err
	}

	if len(iface.IPv4Addrs) == 0 {
		return nil, fmt.Errorf("no IPv4 address found for interface %s", iface.Name)
	}

	ip := net.ParseIP(iface.IPv4Addrs[0])
	if ip == nil {
		return nil, fmt.Errorf("failed to parse IP address: %s", iface.IPv4Addrs[0])
	}

	return &net.TCPAddr{IP: ip, Port: 0}, nil
}

// GetLocalAddrForTarget returns the appropriate local address based on the target address.
// If the target is IPv6, it returns an IPv6 local address. Otherwise, it returns IPv4.
func (im *InterfaceManager) GetLocalAddrForTarget(interfaceType string, targetAddr string) (*net.TCPAddr, error) {
	iface, err := im.GetInterfaceByType(interfaceType)
	if err != nil {
		return nil, err
	}

	// Parse the target address to determine if it's IPv6
	// Remove port if present
	host := targetAddr
	if h, _, err := net.SplitHostPort(targetAddr); err == nil {
		host = h
	}

	targetIP := net.ParseIP(host)
	isIPv6Target := targetIP != nil && targetIP.To4() == nil && targetIP.To16() != nil

	if isIPv6Target {
		// Need to bind to IPv6 address
		if len(iface.IPv6Addrs) == 0 {
			return nil, fmt.Errorf("no IPv6 address found for interface %s (target %s requires IPv6)", iface.Name, host)
		}

		// Find a non-link-local (global) IPv6 address
		var selectedAddr string
		var hasLinkLocal bool
		for _, addr := range iface.IPv6Addrs {
			ip := net.ParseIP(addr)
			if ip == nil {
				continue
			}
			if ip.IsLinkLocalUnicast() {
				hasLinkLocal = true
				continue
			}
			// Found a global IPv6 address
			selectedAddr = addr
			break
		}

		// If only link-local addresses exist, we cannot connect to global IPv6 targets
		if selectedAddr == "" {
			if hasLinkLocal {
				return nil, fmt.Errorf("interface %s only has link-local IPv6 address (cannot connect to global IPv6 target %s)", iface.Name, host)
			}
			return nil, fmt.Errorf("no usable IPv6 address found for interface %s (target %s requires IPv6)", iface.Name, host)
		}

		ip := net.ParseIP(selectedAddr)
		if ip == nil {
			return nil, fmt.Errorf("failed to parse IPv6 address: %s", selectedAddr)
		}
		return &net.TCPAddr{IP: ip, Port: 0}, nil
	}

	// IPv4 target - use IPv4 address
	if len(iface.IPv4Addrs) == 0 {
		return nil, fmt.Errorf("no IPv4 address found for interface %s", iface.Name)
	}

	ip := net.ParseIP(iface.IPv4Addrs[0])
	if ip == nil {
		return nil, fmt.Errorf("failed to parse IP address: %s", iface.IPv4Addrs[0])
	}

	return &net.TCPAddr{IP: ip, Port: 0}, nil
}

// detectInterfaceType attempts to detect the interface type based on name.
func (im *InterfaceManager) detectInterfaceType(name string) string {
	if name == im.cableInterfaceName {
		return "cable"
	}
	if name == im.wifiInterfaceName {
		return "wifi"
	}

	// Common patterns on macOS
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "en") {
		// On macOS, en0 is usually built-in ethernet, en1 is usually Wi-Fi
		// but this can vary by hardware configuration
		return "other"
	}
	if strings.HasPrefix(name, "bridge") || strings.HasPrefix(name, "awdl") ||
		strings.HasPrefix(name, "llw") || strings.HasPrefix(name, "utun") {
		return "virtual"
	}

	return "other"
}

// DetectInterfaces automatically detects cable and wifi interfaces.
func DetectInterfaces() (cable, wifi string, err error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", "", err
	}

	// On macOS, we try to find interfaces by examining their properties
	// This is a heuristic approach and may not work for all systems
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		hasIPv4 := false
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
					hasIPv4 = true
					break
				}
			}
		}

		if !hasIPv4 {
			continue
		}

		// Heuristic: on macOS, en0 is often ethernet, en1 is often wifi
		// We detect based on interface names
		switch iface.Name {
		case "en0":
			if cable == "" {
				cable = iface.Name
			}
		case "en1":
			if wifi == "" {
				wifi = iface.Name
			}
		}
	}

	return cable, wifi, nil
}
