package main

import (
	"bytes"
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

const (
	themeLight = "light"
	themeDark  = "dark"
)

var themePlaceholder = []byte("{{.DefaultTheme}}")

func getenvOr(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func parseBoolEnv(getenv func(string) string, key string, def bool) (bool, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s=%q: %w", key, v, err)
	}
	return b, nil
}

func parseDurationEnv(getenv func(string) string, key string, def time.Duration) (time.Duration, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: %w", key, v, err)
	}
	return d, nil
}

func parseInt64Env(getenv func(string) string, key string, def int64) (int64, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: %w", key, v, err)
	}
	return n, nil
}

// loadConfig reads the nine documented environment variables. The app must
// start with none of them set, so every field has a hard-coded default.
func loadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:         getenvOr(getenv, "BOOKMARKS_ADDR", ":8080"),
		DBPath:       getenvOr(getenv, "BOOKMARKS_DB", "/data/bookmarks.db"),
		DefaultTheme: getenvOr(getenv, "BOOKMARKS_THEME", "dark"),
		UserAgent:    getenvOr(getenv, "BOOKMARKS_USER_AGENT", "Mozilla/5.0 (compatible; bookmarks/1.0)"),
	}

	allowPrivate, err := parseBoolEnv(getenv, "BOOKMARKS_ALLOW_PRIVATE_NET", false)
	if err != nil {
		return Config{}, err
	}
	cfg.AllowPrivateNet = allowPrivate

	fetchTimeout, err := parseDurationEnv(getenv, "BOOKMARKS_FETCH_TIMEOUT", 8*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg.FetchTimeout = fetchTimeout

	maxFetchBytes, err := parseInt64Env(getenv, "BOOKMARKS_MAX_FETCH_BYTES", 1048576)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxFetchBytes = maxFetchBytes

	maxFaviconBytes, err := parseInt64Env(getenv, "BOOKMARKS_MAX_FAVICON_BYTES", 65536)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxFaviconBytes = maxFaviconBytes

	maxImportBytes, err := parseInt64Env(getenv, "BOOKMARKS_MAX_IMPORT_BYTES", 33554432)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxImportBytes = maxImportBytes

	if cfg.DefaultTheme != themeLight && cfg.DefaultTheme != themeDark {
		return Config{}, fmt.Errorf("BOOKMARKS_THEME must be %q or %q, got %q", themeLight, themeDark, cfg.DefaultTheme)
	}

	return cfg, nil
}

// runHealthcheck dials the local /healthz endpoint and reports an exit code.
// This is what makes HEALTHCHECK work on scratch, which has no shell and no
// curl: the binary itself is the health-check client.
func runHealthcheck(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func newServer(cfg Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// buildIndexHTML renders web/index.html once at startup so a browser never
// sees the default theme flash before switching to the configured one; the
// rejected alternative is applying the theme in JS, which does exactly that.
//
// A plain substitution rather than html/template: that package and its
// text/template dependency cost about 4 MB of binary, roughly 40% of the
// image, to interpolate a single attribute value. loadConfig has already
// constrained theme to themeLight or themeDark, so there is nothing to escape.
func buildIndexHTML(assets fs.FS, theme string) ([]byte, error) {
	if theme != themeLight && theme != themeDark {
		return nil, fmt.Errorf("refusing to render index with unvalidated theme %q", theme)
	}
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(data, themePlaceholder) {
		return nil, fmt.Errorf("index.html is missing the %s placeholder", themePlaceholder)
	}
	return bytes.ReplaceAll(data, themePlaceholder, []byte(theme)), nil
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check server health against BOOKMARKS_ADDR and exit")
	flag.Parse()

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	if *healthcheck {
		os.Exit(runHealthcheck(cfg.Addr))
	}

	db, err := openDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("failed to load embedded assets: %v", err)
	}
	indexHTML, err := buildIndexHTML(assets, cfg.DefaultTheme)
	if err != nil {
		log.Fatalf("failed to render index template: %v", err)
	}

	app, err := newApp(db, cfg, assets, indexHTML)
	if err != nil {
		log.Fatalf("failed to initialize app: %v", err)
	}

	server := newServer(cfg, app.Routes())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown error: %v", err)
		}
	}()

	log.Printf("bookmarks listening on %s", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
