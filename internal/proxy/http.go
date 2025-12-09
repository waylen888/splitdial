package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/waylen888/splitdial/internal/logging"
	"github.com/waylen888/splitdial/internal/network"
	"github.com/waylen888/splitdial/internal/router"
)

// HTTPProxyServer implements an HTTP CONNECT proxy server.
type HTTPProxyServer struct {
	addr     string
	router   *router.Router
	dialer   *network.InterfaceDialer
	listener net.Listener
	mu       sync.Mutex
	running  bool
}

// NewHTTPProxyServer creates a new HTTP proxy server.
func NewHTTPProxyServer(addr string, router *router.Router, dialer *network.InterfaceDialer) *HTTPProxyServer {
	return &HTTPProxyServer{
		addr:   addr,
		router: router,
		dialer: dialer,
	}
}

// Start starts the HTTP proxy server.
func (h *HTTPProxyServer) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", h.addr)
	if err != nil {
		return fmt.Errorf("failed to start HTTP proxy: %w", err)
	}

	h.mu.Lock()
	h.listener = listener
	h.running = true
	h.mu.Unlock()

	logging.Info("HTTP proxy listening", "addr", h.addr)

	go func() {
		<-ctx.Done()
		h.Stop()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			h.mu.Lock()
			running := h.running
			h.mu.Unlock()

			if !running {
				return nil
			}
			logging.Error("Failed to accept connection", "error", err)
			continue
		}

		go h.handleConnection(conn)
	}
}

// Stop stops the HTTP proxy server.
func (h *HTTPProxyServer) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.running = false
	if h.listener != nil {
		return h.listener.Close()
	}
	return nil
}

// handleConnection handles a single HTTP proxy client connection.
func (h *HTTPProxyServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(30 * time.Second))

	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		logging.Debug("Failed to read request", "error", err)
		return
	}

	if req.Method == http.MethodConnect {
		h.handleConnect(conn, req)
	} else {
		h.handleHTTP(conn, req, reader)
	}
}

// handleConnect handles HTTPS CONNECT requests.
func (h *HTTPProxyServer) handleConnect(conn net.Conn, req *http.Request) {
	host, portStr, err := net.SplitHostPort(req.Host)
	if err != nil {
		host = req.Host
		portStr = "443"
	}

	port, _ := strconv.Atoi(portStr)
	result := h.router.Route(host, port)
	logging.Info("CONNECT request", "host", req.Host, "interface", result.Interface, "rule", result.RuleName)

	target := net.JoinHostPort(host, portStr)
	remote, err := h.dialer.DialTCP(target, result.Interface)
	if err != nil {
		http.Error(responseWriter{conn}, "Bad Gateway", http.StatusBadGateway)
		logging.Warn("Failed to connect", "target", target, "error", err)
		return
	}
	defer remote.Close()

	// Send 200 Connection Established
	conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	conn.SetDeadline(time.Time{})

	// Relay data
	h.relay(conn, remote)
}

// handleHTTP handles regular HTTP requests.
func (h *HTTPProxyServer) handleHTTP(conn net.Conn, req *http.Request, reader *bufio.Reader) {
	host := req.Host
	portStr := "80"

	if hostPort := strings.Split(host, ":"); len(hostPort) == 2 {
		host = hostPort[0]
		portStr = hostPort[1]
	}

	port, _ := strconv.Atoi(portStr)
	result := h.router.Route(host, port)
	logging.Info("HTTP request", "method", req.Method, "url", req.URL.String(), "interface", result.Interface, "rule", result.RuleName)

	target := net.JoinHostPort(host, portStr)
	remote, err := h.dialer.DialTCP(target, result.Interface)
	if err != nil {
		http.Error(responseWriter{conn}, "Bad Gateway", http.StatusBadGateway)
		logging.Warn("Failed to connect", "target", target, "error", err)
		return
	}
	defer remote.Close()

	// Forward the request
	if err := req.Write(remote); err != nil {
		logging.Debug("Failed to write request", "error", err)
		return
	}

	conn.SetDeadline(time.Time{})

	// Relay response
	io.Copy(conn, remote)
}

// relay relays data between two connections.
func (h *HTTPProxyServer) relay(client, remote net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(remote, client)
	}()

	go func() {
		defer wg.Done()
		io.Copy(client, remote)
	}()

	wg.Wait()
}

// responseWriter wraps a net.Conn to implement http.ResponseWriter minimally.
type responseWriter struct {
	conn net.Conn
}

func (rw responseWriter) Header() http.Header {
	return http.Header{}
}

func (rw responseWriter) Write(data []byte) (int, error) {
	return rw.conn.Write(data)
}

func (rw responseWriter) WriteHeader(statusCode int) {
	fmt.Fprintf(rw.conn, "HTTP/1.1 %d %s\r\n\r\n", statusCode, http.StatusText(statusCode))
}
