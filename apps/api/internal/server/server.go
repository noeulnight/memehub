package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/storage"
	"golang.org/x/oauth2"
)

type Server struct {
	store    *storage.Store
	cfg      config.Config
	db       *pgxpool.Pool
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}

func New(ctx context.Context, cfg config.Config, db *pgxpool.Pool) (*Server, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	if cfg.DemoMode {
		if cfg.Env != "development" && cfg.Env != "test" {
			return nil, errors.New("demo mode is only allowed in development or test")
		}
		store, err := storage.NewLocal(cfg)
		if err != nil {
			return nil, errors.New("cannot initialize demo data directory")
		}
		slog.Warn("demo mode enabled: shared demo administrator, local file storage, and no external analysis")
		return &Server{store: store, cfg: cfg, db: db, client: client}, nil
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), cfg.OIDCIssuer)
	if err != nil {
		return nil, errors.New("OIDC discovery failed")
	}
	return &Server{store: storage.New(cfg), cfg: cfg, db: db, oauth: oauth2.Config{ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret, Endpoint: provider.Endpoint(), RedirectURL: cfg.OIDCRedirectURL, Scopes: []string{oidc.ScopeOpenID, "profile"}}, verifier: provider.VerifierContext(oidc.ClientContext(ctx, client), &oidc.Config{ClientID: cfg.OIDCClientID}), client: client}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.cfg.DemoMode && s.store != nil {
		mux.Handle("/api/demo/storage/", s.store.LocalHandler())
	}
	mux.HandleFunc("GET /api/health/live", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.Ping(ctx); err != nil {
			problem(w, 503, "database_unavailable")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/auth/login", s.login)
	mux.HandleFunc("GET /api/auth/callback", s.callback)
	mux.HandleFunc("GET /api/auth/me", s.me)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/sitemap.xml", s.sitemap)
	mux.HandleFunc("GET /api/tags", s.listTags)
	mux.HandleFunc("GET /api/memes", s.listMemes)
	mux.HandleFunc("GET /api/search/suggestions", s.searchSuggestions)
	mux.HandleFunc("GET /api/collections", s.listCollections)
	mux.HandleFunc("POST /api/collections", s.createCollection)
	mux.HandleFunc("GET /api/collections/{collectionID}", s.getCollection)
	mux.HandleFunc("PUT /api/collections/{collectionID}", s.updateCollection)
	mux.HandleFunc("DELETE /api/collections/{collectionID}", s.deleteCollection)
	mux.HandleFunc("PUT /api/collections/{collectionID}/memes/{id}", s.collectionMeme)
	mux.HandleFunc("DELETE /api/collections/{collectionID}/memes/{id}", s.collectionMeme)
	mux.HandleFunc("GET /api/memes/{id}", s.getMeme)
	mux.HandleFunc("POST /api/memes", s.createMeme)
	mux.HandleFunc("PUT /api/memes/{id}", s.updateMeme)
	mux.HandleFunc("DELETE /api/memes/{id}", s.deleteMeme)
	mux.HandleFunc("POST /api/memes/{id}/upload-url", s.renewUpload)
	mux.HandleFunc("POST /api/memes/{id}/upload-completion", s.completeUpload)
	mux.HandleFunc("GET /api/memes/{id}/media", s.media)
	mux.HandleFunc("GET /api/memes/{id}/file", s.originalMedia)
	mux.HandleFunc("GET /api/admin/memes", s.listAdminMemes)
	mux.HandleFunc("GET /api/admin/users", s.listUsers)
	mux.HandleFunc("GET /api/admin/settings", s.readSettings)
	mux.HandleFunc("PUT /api/admin/settings", s.updateSettings)
	mux.HandleFunc("GET /api/admin/memes/{id}/analyses", s.listAnalyses)
	mux.HandleFunc("POST /api/admin/memes/{id}/analyses", s.retryAnalysis)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		defer func() {
			if recover() != nil {
				slog.Error("request handler panicked")
				problem(w, 500, "internal_error")
			}
		}()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
func internal(w http.ResponseWriter) {
	slog.Error("database operation failed")
	problem(w, 500, "internal_error")
}
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		problem(w, 415, "application_json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		problem(w, 400, "invalid_json")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		problem(w, 400, "invalid_json")
		return false
	}
	return true
}
