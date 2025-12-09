package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/waylen888/splitdial/internal/logging"
	"github.com/waylen888/splitdial/internal/network"
	"github.com/waylen888/splitdial/internal/router"
)

const (
	socks5Version = 0x05

	// Authentication methods
	authNone     = 0x00
	authPassword = 0x02
	authNoAccept = 0xFF

	// Commands
	cmdConnect      = 0x01
	cmdBind         = 0x02
	cmdUDPAssociate = 0x03

	// Address types
	addrTypeIPv4   = 0x01
	addrTypeDomain = 0x03
	addrTypeIPv6   = 0x04

	// Reply codes
	repSuccess              = 0x00
	repGeneralFailure       = 0x01
	repConnectionNotAllowed = 0x02
	repNetworkUnreachable   = 0x03
	repHostUnreachable      = 0x04
	repConnectionRefused    = 0x05
	repTTLExpired           = 0x06
	repCommandNotSupported  = 0x07
	repAddressNotSupported  = 0x08
)

// SOCKS5Server implements a SOCKS5 proxy server.
type SOCKS5Server struct {
	addr           string
	router         *router.Router
	dialer         *network.InterfaceDialer
	listener       net.Listener
	mu             sync.Mutex
	running        bool
	connectionPool sync.Pool
}

// NewSOCKS5Server creates a new SOCKS5 proxy server.
func NewSOCKS5Server(addr string, router *router.Router, dialer *network.InterfaceDialer) *SOCKS5Server {
	return &SOCKS5Server{
		addr:   addr,
		router: router,
		dialer: dialer,
	}
}

// Start starts the SOCKS5 server.
func (s *SOCKS5Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to start SOCKS5 server: %w", err)
	}

	s.mu.Lock()
	s.listener = listener
	s.running = true
	s.mu.Unlock()

	logging.Info("SOCKS5 server listening", "addr", s.addr)

	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			s.mu.Lock()
			running := s.running
			s.mu.Unlock()

			if !running {
				return nil
			}
			logging.Error("Failed to accept connection", "error", err)
			continue
		}

		go s.handleConnection(conn)
	}
}

// Stop stops the SOCKS5 server.
func (s *SOCKS5Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.running = false
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// handleConnection handles a single SOCKS5 client connection.
func (s *SOCKS5Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Set read deadline for handshake
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Step 1: Version and methods negotiation
	if err := s.handleHandshake(conn); err != nil {
		logging.Debug("Handshake failed", "error", err)
		return
	}

	// Step 2: Handle client request
	targetAddr, port, err := s.handleRequest(conn)
	if err != nil {
		logging.Debug("Request handling failed", "error", err)
		return
	}

	// Clear deadline for data transfer
	conn.SetDeadline(time.Time{})

	// Step 3: Route and connect
	result := s.router.Route(targetAddr, port)
	logging.Info("Routing connection", "target", targetAddr, "port", port, "interface", result.Interface, "rule", result.RuleName)

	target := net.JoinHostPort(targetAddr, strconv.Itoa(port))
	remote, err := s.dialer.DialTCP(target, result.Interface)
	if err != nil {
		s.sendReply(conn, repHostUnreachable, "0.0.0.0", 0)
		logging.Warn("Failed to connect", "target", target, "error", err)
		return
	}
	defer remote.Close()

	// Send success reply
	localAddr := remote.LocalAddr().(*net.TCPAddr)
	s.sendReply(conn, repSuccess, localAddr.IP.String(), localAddr.Port)

	// Step 4: Relay data
	s.relay(conn, remote)
}

// handleHandshake handles SOCKS5 authentication handshake.
func (s *SOCKS5Server) handleHandshake(conn net.Conn) error {
	// Read version and number of methods
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}

	if header[0] != socks5Version {
		return fmt.Errorf("unsupported SOCKS version: %d", header[0])
	}

	numMethods := int(header[1])
	methods := make([]byte, numMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return fmt.Errorf("failed to read methods: %w", err)
	}

	// Check for no-auth method
	hasNoAuth := false
	for _, m := range methods {
		if m == authNone {
			hasNoAuth = true
			break
		}
	}

	if !hasNoAuth {
		conn.Write([]byte{socks5Version, authNoAccept})
		return errors.New("no acceptable auth method")
	}

	// Accept no-auth
	_, err := conn.Write([]byte{socks5Version, authNone})
	return err
}

// handleRequest handles SOCKS5 connection request.
func (s *SOCKS5Server) handleRequest(conn net.Conn) (string, int, error) {
	// Read request header
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", 0, fmt.Errorf("failed to read request: %w", err)
	}

	if header[0] != socks5Version {
		return "", 0, fmt.Errorf("unsupported SOCKS version: %d", header[0])
	}

	if header[1] != cmdConnect {
		s.sendReply(conn, repCommandNotSupported, "0.0.0.0", 0)
		return "", 0, fmt.Errorf("unsupported command: %d", header[1])
	}

	// Read address
	var addr string
	switch header[3] {
	case addrTypeIPv4:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", 0, err
		}
		addr = net.IP(ip).String()

	case addrTypeDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return "", 0, err
		}
		domain := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", 0, err
		}
		addr = string(domain)

	case addrTypeIPv6:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", 0, err
		}
		addr = net.IP(ip).String()

	default:
		s.sendReply(conn, repAddressNotSupported, "0.0.0.0", 0)
		return "", 0, fmt.Errorf("unsupported address type: %d", header[3])
	}

	// Read port
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", 0, err
	}
	port := int(binary.BigEndian.Uint16(portBuf))

	return addr, port, nil
}

// sendReply sends a SOCKS5 reply.
func (s *SOCKS5Server) sendReply(conn net.Conn, rep byte, addr string, port int) {
	ip := net.ParseIP(addr)
	var addrType byte
	var addrBytes []byte

	if ip4 := ip.To4(); ip4 != nil {
		addrType = addrTypeIPv4
		addrBytes = ip4
	} else if ip6 := ip.To16(); ip6 != nil {
		addrType = addrTypeIPv6
		addrBytes = ip6
	} else {
		addrType = addrTypeIPv4
		addrBytes = []byte{0, 0, 0, 0}
	}

	reply := make([]byte, 4+len(addrBytes)+2)
	reply[0] = socks5Version
	reply[1] = rep
	reply[2] = 0x00 // reserved
	reply[3] = addrType
	copy(reply[4:], addrBytes)
	binary.BigEndian.PutUint16(reply[4+len(addrBytes):], uint16(port))

	conn.Write(reply)
}

// relay relays data between client and remote.
func (s *SOCKS5Server) relay(client, remote net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	copyFunc := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		// Close write side to signal EOF
		if tcpConn, ok := dst.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}

	go copyFunc(remote, client)
	go copyFunc(client, remote)

	wg.Wait()
}

// Addr returns the address the server is listening on.
func (s *SOCKS5Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}
