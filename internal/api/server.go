package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/waylen888/splitdial/internal/config"
	"github.com/waylen888/splitdial/internal/logging"
	"github.com/waylen888/splitdial/internal/network"
	"github.com/waylen888/splitdial/internal/router"
)

// Server provides a REST API for managing the proxy.
type Server struct {
	addr             string
	configManager    *config.ConfigManager
	interfaceManager *network.InterfaceManager
	router           *router.Router
	mux              *http.ServeMux
}

// NewServer creates a new API server.
func NewServer(addr string, cm *config.ConfigManager, im *network.InterfaceManager, r *router.Router) *Server {
	s := &Server{
		addr:             addr,
		configManager:    cm,
		interfaceManager: im,
		router:           r,
		mux:              http.NewServeMux(),
	}
	s.setupRoutes()
	return s
}

// setupRoutes configures API routes.
func (s *Server) setupRoutes() {
	// API endpoints
	s.mux.HandleFunc("/api/interfaces", s.corsMiddleware(s.handleInterfaces))
	s.mux.HandleFunc("/api/rules", s.corsMiddleware(s.handleRules))
	s.mux.HandleFunc("/api/rules/", s.corsMiddleware(s.handleRuleByID))
	s.mux.HandleFunc("/api/status", s.corsMiddleware(s.handleStatus))
	s.mux.HandleFunc("/api/config", s.corsMiddleware(s.handleConfig))
}

// corsMiddleware adds CORS headers.
func (s *Server) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// handleInterfaces returns network interface information.
func (s *Server) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	interfaces, err := s.interfaceManager.ListInterfaces()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, interfaces)
}

// handleRules handles CRUD operations for routing rules.
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := s.configManager.Get()
		s.jsonResponse(w, cfg.Routes)

	case http.MethodPost:
		var rule config.RouteRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if rule.ID == "" {
			http.Error(w, "Rule ID is required", http.StatusBadRequest)
			return
		}

		s.configManager.AddRoute(rule)
		s.updateRouter()

		if err := s.configManager.Save(); err != nil {
			logging.Warn("Failed to save config", "error", err)
		}

		w.WriteHeader(http.StatusCreated)
		s.jsonResponse(w, rule)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRuleByID handles operations on a specific rule.
func (s *Server) handleRuleByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/rules/")
	if id == "" {
		http.Error(w, "Rule ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if s.configManager.RemoveRoute(id) {
			s.updateRouter()
			if err := s.configManager.Save(); err != nil {
				logging.Warn("Failed to save config", "error", err)
			}
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, "Rule not found", http.StatusNotFound)
		}

	case http.MethodGet:
		cfg := s.configManager.Get()
		for _, rule := range cfg.Routes {
			if rule.ID == id {
				s.jsonResponse(w, rule)
				return
			}
		}
		http.Error(w, "Rule not found", http.StatusNotFound)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleStatus returns proxy server status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := s.configManager.Get()
	status := map[string]interface{}{
		"running":    true,
		"socks_addr": cfg.Server.SOCKSAddr,
		"http_addr":  cfg.Server.HTTPAddr,
		"api_addr":   cfg.Server.APIAddr,
		"rules":      len(cfg.Routes),
	}

	s.jsonResponse(w, status)
}

// handleConfig returns or updates the full configuration.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := s.configManager.Get()
		s.jsonResponse(w, cfg)

	case http.MethodPut:
		var cfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		s.configManager.UpdateRoutes(cfg.Routes)
		s.updateRouter()

		if err := s.configManager.Save(); err != nil {
			logging.Warn("Failed to save config", "error", err)
		}

		w.WriteHeader(http.StatusOK)
		s.jsonResponse(w, cfg)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// updateRouter updates the router with new rules.
func (s *Server) updateRouter() {
	cfg := s.configManager.Get()
	s.router.UpdateRules(cfg.Routes)
}

// jsonResponse writes a JSON response.
func (s *Server) jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// Start starts the API server.
func (s *Server) Start() error {
	logging.Info("API server listening", "addr", s.addr)
	return http.ListenAndServe(s.addr, s.mux)
}
