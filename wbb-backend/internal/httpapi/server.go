// Package httpapi exposes the WBB public HTTP API under /api/v1 and maps
// module errors onto the documented error envelope. Handlers stay thin: all
// behaviour lives in the auth and broadcasts modules behind this seam.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
	"github.com/impwy/wbb-live/wbb-backend/internal/broadcasts"
	"github.com/impwy/wbb-live/wbb-backend/internal/comments"
	"github.com/impwy/wbb-live/wbb-backend/internal/config"
	"github.com/impwy/wbb-live/wbb-backend/internal/livesessions"
	"github.com/impwy/wbb-live/wbb-backend/internal/mediacompletion"
	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
)

// Server wires modules into HTTP routes.
type Server struct {
	cfg          config.Config
	auth         *auth.Service
	broadcasts   *broadcasts.Service
	sessions     *livesessions.Service
	redis        redis.UniversalClient
	db           *pgxpool.Pool
	log          *slog.Logger
	googleClient *http.Client
	mediaClient  *http.Client
	turnClient   *http.Client
	completion   *mediacompletion.Client
	comments     *comments.Service

	// rateLimitClock is injectable so tests can pin the fixed-window bucket;
	// production uses time.Now.
	rateLimitClock func() time.Time
}

func NewServer(cfg config.Config, authSvc *auth.Service, bcastSvc *broadcasts.Service, rdb redis.UniversalClient, dbPool *pgxpool.Pool, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	sessions := livesessions.NewService(dbPool, auth.NewTokenManager(cfg.JWTSecret, cfg.AccessTokenTTL))
	if cfg.MediaWHIPURL != "" {
		sessions.EnableMedia()
	}
	var completion *mediacompletion.Client
	if cfg.MediaCompletionURL != "" {
		completion, _ = mediacompletion.NewClient(cfg.MediaCompletionURL, cfg.MediaCompletionKey, cfg.MediaCompletionRoot, cfg.MediaCompletionSlot)
	}
	return &Server{cfg: cfg, auth: authSvc, broadcasts: bcastSvc, sessions: sessions, redis: rdb, db: dbPool, log: log, rateLimitClock: time.Now, googleClient: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, mediaClient: privateMediaClient(), turnClient: &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, completion: completion, comments: comments.NewService(dbPool)}
}

// RunMaintenance reconciles durable preparation timeouts independently of
// public reads and Redis. The caller owns cancellation on server shutdown.
func (s *Server) RunMaintenance(ctx context.Context) {
	s.sessions.RunMaintenance(ctx, func(err error) { s.log.Error("session maintenance", "error", err) })
}

// SetSessionClock is an integration-test clock seam; production never calls it.
func (s *Server) SetSessionClock(now func() time.Time) { s.sessions.SetClock(now) }

// SetGoogleHTTPClient changes only the outbound transport, for integration
// tests of Google's fixed endpoints. Production configuration cannot change
// issuer or endpoints.
func (s *Server) SetGoogleHTTPClient(client *http.Client) { s.googleClient = client }

// SetRateLimitClock pins the rate-limit window clock (tests only).
func (s *Server) SetRateLimitClock(now func() time.Time) { s.rateLimitClock = now }

const maxBodyBytes = 64 << 10 // 64 KiB request body cap (docs/design.md §5)

// Handler returns the fully middleware-chained HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /api/v1/health/ready", s.handleReady)
	mux.HandleFunc("POST /internal/mediamtx/auth", s.handleMediaMTXAuth)

	mux.HandleFunc("POST /api/v1/auth/register", s.rateLimit("register", requireOrigin(s, s.handleRegister)))
	mux.HandleFunc("POST /api/v1/auth/login", s.rateLimit("login", requireOrigin(s, s.handleLogin)))
	if s.cfg.EvaluationLoginEnabled {
		mux.HandleFunc("POST /api/v1/auth/evaluation-login", requireOrigin(s, s.handleEvaluationLogin))
	}
	mux.HandleFunc("POST /api/v1/auth/refresh", s.rateLimit("refresh", requireOrigin(s, s.handleRefresh)))
	mux.HandleFunc("POST /api/v1/auth/logout", requireOrigin(s, s.handleLogout))
	mux.HandleFunc("GET /api/v1/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("GET /api/v1/media/ice-servers", s.requireAuth(s.handleICEServers))
	mux.HandleFunc("GET /api/v1/auth/providers", s.handleProviders)
	mux.HandleFunc("GET /api/v1/auth/google/start", s.googleHeaders(s.rateLimit("google-start", s.handleGoogleStart)))
	mux.HandleFunc("GET /api/v1/auth/google/callback", s.googleHeaders(s.rateLimit("google-callback", s.handleGoogleCallback)))

	mux.HandleFunc("GET /api/v1/broadcasts", s.handleListBroadcasts)
	mux.HandleFunc("POST /api/v1/broadcasts", s.requireAuth(s.handleCreateBroadcast))
	mux.HandleFunc("GET /api/v1/broadcasts/{id}", s.handleGetBroadcast)
	mux.HandleFunc("PATCH /api/v1/broadcasts/{id}", s.requireAuth(s.handleUpdateBroadcast))
	mux.HandleFunc("DELETE /api/v1/broadcasts/{id}", s.requireAuth(s.handleDeleteBroadcast))
	mux.HandleFunc("POST /api/v1/broadcasts/{id}/sessions", s.requireAuth(requireOrigin(s, s.handleStartSession)))
	mux.HandleFunc("GET /api/v1/broadcasts/{id}/sessions/current", s.handleCurrentSession)
	mux.HandleFunc("GET /api/v1/broadcasts/{id}/playback", s.handlePlayback)
	mux.HandleFunc("GET /api/v1/broadcasts/{id}/comments", s.handleListComments)
	mux.HandleFunc("POST /api/v1/broadcasts/{id}/comments", s.requireAuth(requireOrigin(s, s.handleCreateComment)))
	mux.HandleFunc("PATCH /api/v1/comments/{cid}", s.requireAuth(requireOrigin(s, s.handleUpdateComment)))
	mux.HandleFunc("DELETE /api/v1/comments/{cid}", s.requireAuth(requireOrigin(s, s.handleDeleteComment)))
	mux.HandleFunc("GET /internal/hls-authorize", s.handleHLSAuthorize)
	mux.HandleFunc("POST /api/v1/broadcasts/{id}/sessions/{sid}/stop", s.requireAuth(requireOrigin(s, s.handleStopSession)))
	mux.HandleFunc("POST /api/v1/broadcasts/{id}/sessions/{sid}/publish-grants", s.requireAuth(requireOrigin(s, s.handleRenewGrant)))
	mux.HandleFunc("POST /api/v1/live-sessions/{sid}/whip", requireOrigin(s, s.handleWHIPPost))
	mux.HandleFunc("PATCH /api/v1/live-sessions/{sid}/whip/{resource}", s.requireAuth(requireOrigin(s, s.handleWHIPResource)))
	mux.HandleFunc("DELETE /api/v1/live-sessions/{sid}/whip/{resource}", s.requireAuth(requireOrigin(s, s.handleWHIPResource)))

	var root http.Handler
	if s.cfg.StaticDir != "" {
		root = spaHandler(s.cfg.StaticDir)
	} else {
		root = http.NotFoundHandler()
	}
	mux.Handle("/", root)

	return s.withRequestID(s.recoverPanic(s.withCORS(mux)))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "Database is not reachable.")
		return
	}
	latest, err := db.LatestApplied(ctx, s.db)
	if err != nil || latest != db.LatestEmbedded() {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "Migrations are not up to date.")
		return
	}
	if s.cfg.MediaWHIPURL != "" {
		if s.completion == nil {
			writeError(w, r, http.StatusServiceUnavailable, "not_ready", "Media completion is not configured.")
			return
		}
		mediaCtx, mediaCancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer mediaCancel()
		if _, err := s.completion.Lifecycle(mediaCtx); err != nil {
			writeError(w, r, http.StatusServiceUnavailable, "not_ready", "Media completion is unavailable.")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// spaHandler serves the built Flutter Web bundle with an index.html fallback
// for client-side routes. API paths never reach it.
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if p == "." || p == "" {
			p = "index.html"
		}
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			r.URL.Path = "/"
		}
		fs.ServeHTTP(w, r)
	})
}
