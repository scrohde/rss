// Package main wires dependencies and runs the Pulse RSS web server.
package main

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"rss/internal/server"
	"rss/internal/store"
)

const (
	serverReadTimeout  = 10 * time.Second
	serverWriteTimeout = 10 * time.Second
	serverIdleTimeout  = 60 * time.Second
	authSessionTTL     = 24 * time.Hour
	authChallengeTTL   = 5 * time.Minute
)

var (
	errAuthRPIDRequired      = errors.New("AUTH_RP_ID is required when AUTH_ENABLED=true")
	errAuthRPOriginRequired  = errors.New("AUTH_RP_ORIGIN is required when AUTH_ENABLED=true")
	errAuthSetupTokenMissing = errors.New("AUTH_SETUP_TOKEN is required when AUTH_ENABLED=true")
)

//go:embed templates/*.html templates/partials/*.html
var templateFiles embed.FS

//go:embed static
var staticFiles embed.FS

func main() {
	err := run()
	if err != nil {
		log.Fatal(err)
	}
}

func run() error {
	setupLogging()

	db, err := openInitializedDB("rss.db")
	if err != nil {
		return err
	}

	defer func() {
		closeDB(db)
	}()

	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html", "templates/partials/*.html"))

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("open embedded static files: %w", err)
	}

	app, err := configureApp(db, tmpl, staticFS)
	if err != nil {
		return err
	}

	app.StartBackgroundLoops()

	return serve(app)
}

func openInitializedDB(path string) (*sql.DB, error) {
	db, err := store.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	err = store.Init(db)
	if err != nil {
		return nil, fmt.Errorf("initialize database: %w", err)
	}

	return db, nil
}

func closeDB(db *sql.DB) {
	closeErr := db.Close()
	if closeErr != nil {
		log.Printf("db.Close: %v", closeErr)
	}
}

func configureApp(db *sql.DB, tmpl *template.Template, staticFS fs.FS) (*server.App, error) {
	app := server.New(db, tmpl)
	app.SetStaticFS(staticFS)

	authCfg, err := resolveAuthConfig()
	if err != nil {
		return nil, err
	}

	authErr := app.SetAuthConfig(&authCfg)
	if authErr != nil {
		return nil, fmt.Errorf("configure auth: %w", authErr)
	}

	return app, nil
}

func serve(app *server.App) error {
	httpServer := new(http.Server)
	httpServer.Addr = resolveAddr()
	httpServer.Handler = app.Routes()
	httpServer.ReadTimeout = serverReadTimeout
	httpServer.WriteTimeout = serverWriteTimeout
	httpServer.IdleTimeout = serverIdleTimeout

	slog.Info("rss reader running", "addr", httpServer.Addr)

	err := httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}

	return nil
}

func setupLogging() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	options := new(slog.HandlerOptions)
	options.Level = resolveLogLevel()
	handler := slog.NewTextHandler(os.Stdout, options)
	slog.SetDefault(slog.New(handler))
}

func resolveLogLevel() slog.Level {
	const defaultLevel = slog.LevelInfo

	raw := strings.TrimSpace(os.Getenv("LOG_LEVEL"))
	if raw == "" {
		return defaultLevel
	}

	normalized := strings.ToLower(raw)
	if normalized == "warning" {
		normalized = "warn"
	}

	var level slog.Level

	err := level.UnmarshalText([]byte(normalized))
	if err != nil {
		log.Printf("invalid LOG_LEVEL value; defaulting to %s", defaultLevel)

		return defaultLevel
	}

	return level
}

func resolveAddr() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		return "127.0.0.1:8080"
	}

	if strings.HasPrefix(port, ":") {
		return "127.0.0.1" + port
	}

	_, err := strconv.Atoi(port)
	if err != nil {
		return "127.0.0.1:8080"
	}

	return "127.0.0.1:" + port
}

func envBool(name string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch raw {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func resolveAuthConfig() (server.AuthConfig, error) {
	enabled := envBool("AUTH_ENABLED")

	cfg := server.AuthConfig{
		Enabled:      enabled,
		RPID:         strings.TrimSpace(os.Getenv("AUTH_RP_ID")),
		RPOrigin:     strings.TrimSpace(os.Getenv("AUTH_RP_ORIGIN")),
		RPName:       strings.TrimSpace(os.Getenv("AUTH_RP_NAME")),
		SetupToken:   strings.TrimSpace(os.Getenv("AUTH_SETUP_TOKEN")),
		SessionTTL:   envDuration("AUTH_SESSION_TTL", authSessionTTL),
		ChallengeTTL: envDuration("AUTH_CHALLENGE_TTL", authChallengeTTL),
		CookieName:   strings.TrimSpace(os.Getenv("AUTH_COOKIE_NAME")),
		CookieSecure: true,
	}

	if raw := strings.TrimSpace(os.Getenv("AUTH_COOKIE_SECURE")); raw != "" {
		cfg.CookieSecure = envBool("AUTH_COOKIE_SECURE")
	} else if !enabled {
		cfg.CookieSecure = false
	}

	if cfg.RPName == "" {
		cfg.RPName = "Pulse RSS"
	}

	if !enabled {
		return cfg, nil
	}

	if cfg.RPID == "" {
		return server.AuthConfig{}, errAuthRPIDRequired
	}

	if cfg.RPOrigin == "" {
		return server.AuthConfig{}, errAuthRPOriginRequired
	}

	if cfg.SetupToken == "" {
		return server.AuthConfig{}, errAuthSetupTokenMissing
	}

	return cfg, nil
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}

	return parsed
}
