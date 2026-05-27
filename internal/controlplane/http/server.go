package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	stdhttp "net/http"
	"strings"

	"stackforge/internal/controlplane/auth"
	cpconfig "stackforge/internal/controlplane/config"
	"stackforge/internal/controlplane/domain"
	"stackforge/internal/controlplane/reconcile"
)

type Server struct {
	cfg   cpconfig.Config
	auth  *auth.Authenticator
	store domain.Repository
	log   *slog.Logger
}

func New(cfg cpconfig.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{cfg: cfg, auth: auth.New(cfg.AdminAPIKeys), store: domain.NewStore(), log: logger}
}

func NewPersistent(ctx context.Context, cfg cpconfig.Config, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Env == "production" && cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required in production mode")
	}
	store := domain.Repository(domain.NewStore())
	if cfg.DatabaseURL != "" {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.DatabaseURL)), "sqlite:") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.DatabaseURL)), "file:") || strings.HasSuffix(strings.ToLower(strings.TrimSpace(cfg.DatabaseURL)), ".db") {
			sqliteStore, err := domain.NewSQLiteStore(ctx, cfg.DatabaseURL)
			if err != nil {
				return nil, err
			}
			store = sqliteStore
		} else {
			pg, err := domain.NewPostgresStore(ctx, cfg.DatabaseURL)
			if err != nil {
				return nil, err
			}
			store = pg
		}
	}
	return &Server{cfg: cfg, auth: auth.New(cfg.AdminAPIKeys), store: store, log: logger}, nil
}

func (s *Server) Handler() stdhttp.Handler {
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /health", func(w stdhttp.ResponseWriter, r *stdhttp.Request) { writeJSON(w, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /health/nomad", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		writeJSON(w, map[string]string{"status": "unknown", "component": "nomad"})
	})
	mux.HandleFunc("GET /health/consul", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		writeJSON(w, map[string]string{"status": "unknown", "component": "consul"})
	})
	mux.HandleFunc("GET /health/cloudflare", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		writeJSON(w, map[string]string{"status": "unknown", "component": "cloudflare"})
	})
	ready := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if !s.auth.Enabled() {
			stdhttp.Error(w, "admin API keys are required", stdhttp.StatusForbidden)
			return
		}
		writeJSON(w, map[string]string{"status": "ready"})
	})
	if s.cfg.ReadyProtected {
		mux.Handle("GET /ready", s.auth.Middleware(ready))
	} else {
		mux.Handle("GET /ready", ready)
	}
	api := stdhttp.NewServeMux()
	api.HandleFunc("POST /api/v1/domains", s.createDomain)
	api.HandleFunc("GET /api/v1/domains", s.listDomains)
	api.HandleFunc("GET /api/v1/domains/{id}", s.getDomain)
	api.HandleFunc("DELETE /api/v1/domains/{id}", s.deleteDomain)
	api.HandleFunc("PATCH /api/v1/domains/{id}", s.patchDomain)
	api.HandleFunc("POST /api/v1/domains/{id}/verification-token", s.verificationToken)
	api.HandleFunc("POST /api/v1/domains/{id}/verify", s.verifyDomain)
	api.HandleFunc("POST /api/v1/domains/{id}/dns/apply", s.accepted("dns apply"))
	api.HandleFunc("DELETE /api/v1/domains/{id}/dns", s.accepted("dns delete"))
	api.HandleFunc("POST /api/v1/domains/{id}/routing/apply", s.accepted("routing apply"))
	api.HandleFunc("DELETE /api/v1/domains/{id}/routing", s.accepted("routing delete"))
	api.HandleFunc("GET /api/v1/domains/{id}/status", s.getDomain)
	api.HandleFunc("POST /api/v1/domains/{id}/reconcile", s.reconcileDomain)
	api.HandleFunc("POST /api/v1/domains/reconcile-all", s.reconcileAll)
	api.HandleFunc("GET /api/v1/audit-logs", func(w stdhttp.ResponseWriter, r *stdhttp.Request) { writeJSON(w, []any{}) })
	api.HandleFunc("GET /api/v1/domains/{id}/audit-logs", func(w stdhttp.ResponseWriter, r *stdhttp.Request) { writeJSON(w, []any{}) })
	mux.Handle("/api/v1/", s.auth.Middleware(api))
	return requestID(mux)
}

func (s *Server) ListenAndServe() error {
	return stdhttp.ListenAndServe(s.cfg.HTTPAddr, s.Handler())
}

func (s *Server) createDomain(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var d domain.Domain
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	created, err := s.store.Create(d, s.cfg.AllowWildcardDomains)
	if err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	writeJSONStatus(w, created, 201)
}

func (s *Server) listDomains(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	writeJSON(w, s.store.List())
}

func (s *Server) getDomain(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	d, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		stdhttp.NotFound(w, r)
		return
	}
	writeJSON(w, d)
}

func (s *Server) deleteDomain(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		stdhttp.NotFound(w, r)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) patchDomain(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	stdhttp.Error(w, "patch is not enabled for immutable routing fields in this build", 409)
}

func (s *Server) verificationToken(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	v, err := s.store.VerificationToken(r.PathValue("id"))
	if err != nil {
		stdhttp.Error(w, err.Error(), 404)
		return
	}
	writeJSON(w, v)
}

func (s *Server) verifyDomain(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.store.MarkVerified(r.PathValue("id"), body.Token); err != nil {
		stdhttp.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]string{"status": "verified"})
}

func (s *Server) reconcileDomain(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	d, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		stdhttp.NotFound(w, r)
		return
	}
	res := reconcile.Domain(d)
	if res.Error != "" {
		writeJSONStatus(w, res, 409)
		return
	}
	writeJSON(w, res)
}

func (s *Server) reconcileAll(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var out []reconcile.Result
	for _, d := range s.store.List() {
		out = append(out, reconcile.Domain(d))
	}
	writeJSON(w, out)
}

func (s *Server) accepted(action string) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if !strings.Contains(action, "delete") {
			d, ok := s.store.Get(r.PathValue("id"))
			if !ok {
				stdhttp.NotFound(w, r)
				return
			}
			if d.OwnershipStatus != "verified" {
				stdhttp.Error(w, "ownership is not verified", 409)
				return
			}
		}
		writeJSONStatus(w, map[string]string{"status": "accepted", "action": action}, 202)
	}
}

func writeJSON(w stdhttp.ResponseWriter, v any) { writeJSONStatus(w, v, 200) }

func writeJSONStatus(w stdhttp.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func requestID(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.Header.Get("X-Request-ID") == "" {
			r.Header.Set("X-Request-ID", "generated")
		}
		next.ServeHTTP(w, r)
	})
}
