package main

import (
	"errors"
	"html/template"
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

func main() {
	setupLogging()
	db, err := store.Open("rss.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := store.Init(db); err != nil {
		log.Fatal(err)
	}

	tmpl := template.Must(template.ParseGlob("templates/*.html"))
	tmpl = template.Must(tmpl.ParseGlob("templates/partials/*.html"))

	app := server.New(db, tmpl)
	app.SetImageProxyDebug(envBool("IMAGE_PROXY_DEBUG"))
	app.StartBackgroundLoops()

	httpServer := &http.Server{
		Addr:         resolveAddr(),
		Handler:      app.Routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("rss reader running", "addr", httpServer.Addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func setupLogging() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
}

func resolveAddr() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		return ":8080"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ":8080"
	}
	return ":" + port
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
