package network

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/waylen888/splitdial/internal/logging"
)

// InterfaceDialer creates network connections bound to a specific interface.
type InterfaceDialer struct {
	interfaceManager *InterfaceManager
	timeout          time.Duration
}

// NewInterfaceDialer creates a new interface-bound dialer.
func NewInterfaceDialer(im *InterfaceManager, timeout time.Duration) *InterfaceDialer {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &InterfaceDialer{
		interfaceManager: im,
		timeout:          timeout,
	}
}

// DialContext creates a connection to the address using the specified interface.
func (id *InterfaceDialer) DialContext(ctx context.Context, network, address, interfaceType string) (net.Conn, error) {
	// Use GetLocalAddrForTarget to select appropriate IPv4 or IPv6 local address
	localAddr, err := id.interfaceManager.GetLocalAddrForTarget(interfaceType, address)
	if err != nil {
		logging.Warn("Interface unavailable, falling back to default route",
			"interface", interfaceType,
			"target", address,
			"error", err,
		)
		// Fallback: Proceed with nil localAddr (system default)
		localAddr = nil
	}

	logging.Debug("Dialing connection",
		"address", address,
		"interface", interfaceType,
		"local_addr", localAddr.String(),
	)

	dialer := &net.Dialer{
		LocalAddr: localAddr,
		Timeout:   id.timeout,
	}

	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s via %s: %w", address, interfaceType, err)
	}

	return conn, nil
}

// Dial creates a connection to the address using the specified interface.
func (id *InterfaceDialer) Dial(network, address, interfaceType string) (net.Conn, error) {
	return id.DialContext(context.Background(), network, address, interfaceType)
}

// DialTCP creates a TCP connection bound to the specified interface.
func (id *InterfaceDialer) DialTCP(address, interfaceType string) (net.Conn, error) {
	return id.Dial("tcp", address, interfaceType)
}

// DialUDP creates a UDP connection bound to the specified interface.
func (id *InterfaceDialer) DialUDP(address, interfaceType string) (net.Conn, error) {
	return id.Dial("udp", address, interfaceType)
}

// DialerForInterface returns a standard net.Dialer configured for the interface.
func (id *InterfaceDialer) DialerForInterface(interfaceType string) (*net.Dialer, error) {
	localAddr, err := id.interfaceManager.GetLocalAddr(interfaceType)
	if err != nil {
		return nil, err
	}

	return &net.Dialer{
		LocalAddr: localAddr,
		Timeout:   id.timeout,
	}, nil
}
