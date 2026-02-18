// Package main wires dependencies and runs the Pulse RSS web server.
package main

import (
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

	db, err := store.Open("rss.db")
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	defer func() {
		closeErr := db.Close()
		if closeErr != nil {
			log.Printf("db.Close: %v", closeErr)
		}
	}()

	initErr := store.Init(db)
	if initErr != nil {
		return fmt.Errorf("initialize database: %w", initErr)
	}

	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html", "templates/partials/*.html"))

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("open embedded static files: %w", err)
	}

	app := server.New(db, tmpl)
	app.SetStaticFS(staticFS)
	app.SetImageProxyDebug(envBool("IMAGE_PROXY_DEBUG"))
	app.StartBackgroundLoops()

	httpServer := new(http.Server)
	httpServer.Addr = resolveAddr()
	httpServer.Handler = app.Routes()
	httpServer.ReadTimeout = serverReadTimeout
	httpServer.WriteTimeout = serverWriteTimeout
	httpServer.IdleTimeout = serverIdleTimeout

	slog.Info("rss reader running", "addr", httpServer.Addr)

	err = httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}

	return nil
}

func setupLogging() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	options := new(slog.HandlerOptions)
	options.Level = slog.LevelInfo
	handler := slog.NewTextHandler(os.Stdout, options)
	slog.SetDefault(slog.New(handler))
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
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
