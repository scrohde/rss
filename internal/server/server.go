package server

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"rss/internal/auth"
	"rss/internal/content"
	"rss/internal/feed"
	"rss/internal/outbound"
	"rss/internal/store"
)

const (
	feedEditModeCookie             = "pulse_rss_feed_edit_mode"
	maxFormBytes             int64 = 1 << 20
	maxPasskeyJSONBytes      int64 = 256 << 10
	maxOPMLUploadBytes       int64 = 2 << 20
	imageProxySniffBytes           = 512
	cleanupInterval                = 10 * time.Minute
	pulseRecentRefreshWindow       = time.Hour
	feedEditModeCookieMaxAge       = 60 * 60 * 24 * 365
	mobileStreamLimit              = 200
	// Aggregate pagination replaces the current feed batch or one section, capping the mobile DOM at 48 cards.
	mobileAggregateFeedPageLimit = 4
	mobileAggregateItemPageLimit = 12
	mobilePulseTimeout           = 20 * time.Second
)

// App wires handlers, dependencies, and background loops for the HTTP server.
type App struct {
	staticHandler              http.Handler
	markAllReadUndoByToken     map[string]markAllReadUndoState
	authManager                *auth.Manager
	db                         *sql.DB
	tmpl                       *template.Template
	imageProxyClient           *http.Client
	imageProxyLookup           content.LookupIPAddrFunc
	outboundResolver           outbound.Resolver
	authRateLimiter            *authRateLimiter
	authTrustedProxies         []*net.IPNet
	markAllReadUndoTokenByFeed map[int64]string
	pulseStatuses              map[int64]pulseFeedStatusEntry
	authSetupCookieName        string
	authCookieName             string
	authSetupToken             string
	authSetupSignerKey         []byte
	refreshMu                  sync.Mutex
	pulseMu                    sync.Mutex
	markAllReadUndoMu          sync.Mutex
	pulseRunning               bool
	authEnabled                bool
	authCookieSecure           bool
}

// New constructs an App with default static file and image proxy dependencies.
func New(db *sql.DB, tmpl *template.Template) *App {
	app := new(App)
	app.db = db
	app.tmpl = tmpl
	app.staticHandler = http.FileServer(http.Dir("static"))
	app.imageProxyClient = content.NewHTTPClient()
	app.imageProxyLookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}
	app.outboundResolver = net.DefaultResolver
	app.authManager = nil
	app.authRateLimiter = nil
	app.authTrustedProxies = nil
	app.authCookieName = ""
	app.authSetupToken = ""
	app.authSetupCookieName = ""
	app.authSetupSignerKey = nil
	app.refreshMu = sync.Mutex{}
	app.pulseMu = sync.Mutex{}
	app.markAllReadUndoMu = sync.Mutex{}
	app.pulseStatuses = make(map[int64]pulseFeedStatusEntry)
	app.pulseRunning = false
	app.authEnabled = false
	app.authCookieSecure = false
	app.markAllReadUndoByToken = make(map[string]markAllReadUndoState)
	app.markAllReadUndoTokenByFeed = make(map[int64]string)

	return app
}

// SetStaticFS replaces the static file system used for `/static/*` routes.
func (a *App) SetStaticFS(fsys fs.FS) {
	a.staticHandler = http.FileServer(http.FS(fsys))
}

// Routes returns the fully configured application HTTP handler.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	a.registerCoreRoutes(mux)
	a.registerFeedRoutes(mux)

	if a.authEnabled {
		a.registerAuthRoutes(mux)
	}

	var handler http.Handler = mux

	return a.wrapRoutes(handler)
}

// StartBackgroundLoops starts cleanup and feed refresh goroutines.
func (a *App) StartBackgroundLoops() {
	go a.cleanupLoop()
	go a.refreshLoop()
}

func (a *App) registerCoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", a.handleHealthz)
	mux.Handle("GET /static/", http.StripPrefix("/static/", a.staticHandler))
	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /opml/export", a.handleExportOPML)
	mux.HandleFunc("POST /opml/import", a.handleImportOPML)
	mux.HandleFunc("GET "+content.ImageProxyPath, a.handleImageProxy)
}

func (a *App) registerFeedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /feeds", a.handleSubscribe)
	mux.HandleFunc("POST /feeds/pulse", a.handlePulseFeeds)
	mux.HandleFunc("GET /feeds/pulse/status", a.handlePulseStatus)
	mux.HandleFunc("POST /feeds/edit-mode", a.handleEnterFeedEditMode)
	mux.HandleFunc("POST /feeds/edit-mode/save", a.handleSaveFeedEditMode)
	mux.HandleFunc("POST /feeds/edit-mode/cancel", a.handleCancelFeedEditMode)
	mux.HandleFunc("POST /feeds/{feedID}/delete", a.handleDeleteFeed)
	mux.HandleFunc("POST /feeds/{feedID}/refresh", a.handleRefreshFeed)
	mux.HandleFunc("GET /feeds/{feedID}/items", a.handleFeedItems)
	mux.HandleFunc("GET /feeds/{feedID}/items/continue", a.handleContinueFeed)
	mux.HandleFunc("GET /feeds/{feedID}/items/new", a.handleFeedItemsNew)
	mux.HandleFunc("GET /feeds/{feedID}/items/poll", a.handleFeedItemsPoll)
	mux.HandleFunc("POST /feeds/{feedID}/items/read", a.handleMarkAllRead)
	mux.HandleFunc("POST /feeds/{feedID}/items/read/undo", a.handleUndoMarkAllRead)
	mux.HandleFunc("POST /feeds/{feedID}/items/sweep", a.handleSweepRead)
	mux.HandleFunc("GET /items/{itemID}", a.handleItemExpanded)
	mux.HandleFunc("GET /items/{itemID}/compact", a.handleItemCompact)
	mux.HandleFunc("POST /items/{itemID}/toggle", a.handleToggleRead)
	mux.HandleFunc("GET /mobile/stream", a.handleMobileStream)
	mux.HandleFunc("GET /mobile/stream/sections", a.handleMobileStreamSections)
	mux.HandleFunc("GET /mobile/feeds/{feedID}/items", a.handleMobileFeedItems)
	mux.HandleFunc("POST /mobile/feeds/{feedID}/refresh", a.handleMobileRefreshFeed)
	mux.HandleFunc("GET /mobile/items/{itemID}/reader", a.handleMobileReader)
	mux.HandleFunc("POST /mobile/items/{itemID}/read", a.handleMobileMarkRead)
	mux.HandleFunc("POST /mobile/pulse", a.handleMobilePulse)
}

func (a *App) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", a.handleAuthLogin)
	mux.HandleFunc("POST /auth/webauthn/login/options", a.handleAuthLoginOptions)
	mux.HandleFunc("POST /auth/webauthn/login/verify", a.handleAuthLoginVerify)
	mux.HandleFunc("GET /auth/setup", a.handleAuthSetup)
	mux.HandleFunc("POST /auth/setup/unlock", a.handleAuthSetupUnlock)
	mux.HandleFunc("POST /auth/webauthn/register/options", a.handleAuthRegisterOptions)
	mux.HandleFunc("POST /auth/webauthn/register/verify", a.handleAuthRegisterVerify)
	mux.HandleFunc("POST /auth/logout", a.handleAuthLogout)
	mux.HandleFunc("POST /auth/theme", a.handleAuthTheme)
	mux.HandleFunc("GET /auth/security", a.handleAuthSecurity)
	mux.HandleFunc("GET /auth/recovery", a.handleAuthRecovery)
	mux.HandleFunc("POST /auth/recovery/use", a.handleAuthRecoveryUse)
	mux.HandleFunc("POST /auth/recovery/generate", a.handleAuthRecoveryGenerate)
}

func (a *App) wrapRoutes(handler http.Handler) http.Handler {
	handler = a.withSecurityHeaders(handler)

	if a.authEnabled {
		handler = a.withAuthRateLimit(handler)
		handler = a.withCSRFMiddleware(handler)
		handler = a.withAuthSession(handler)
	}

	handler = a.withRealIP(handler)
	handler = a.withRequestID(handler)

	return handler
}

func (a *App) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		a.runCleanupIteration()

		<-ticker.C
	}
}

func (a *App) runCleanupIteration() {
	err := store.CleanupReadItems(a.db)
	if err != nil {
		slog.Error("cleanup error", "err", err)
	}

	tombstoneErr := store.CleanupTombstones(a.db)
	if tombstoneErr != nil {
		slog.Error("tombstone cleanup error", "err", tombstoneErr)
	}

	if a.authEnabled && a.authManager != nil {
		authErr := a.authManager.CleanupExpiredAuthData(context.Background())
		if authErr != nil {
			slog.Error("auth cleanup error", "err", authErr)
		}
	}
}

func (a *App) refreshLoop() {
	ticker := time.NewTicker(feed.RefreshLoopInterval)
	defer ticker.Stop()

	for {
		err := a.refreshDueFeeds()
		if err != nil {
			slog.Error("refresh loop error", "err", err)
		}

		<-ticker.C
	}
}

func (a *App) refreshDueFeeds() error {
	if a.isPulseRunning() {
		return nil
	}

	ids, err := store.ListDueFeeds(a.db, time.Now().UTC(), feed.RefreshBatchSize)
	if err != nil {
		return fmt.Errorf("list due feeds: %w", err)
	}

	if len(ids) > 0 {
		slog.Info("refresh due feeds", "count", len(ids))
	}

	for _, id := range ids {
		a.refreshMu.Lock()
		_, refreshErr := feed.Refresh(context.Background(), a.db, id)
		a.refreshMu.Unlock()

		if refreshErr != nil {
			slog.Error("refresh feed error", "feed_id", id, "err", refreshErr)
		}
	}

	return nil
}
