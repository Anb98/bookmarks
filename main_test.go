package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func fakeGetenv(vals map[string]string) func(string) string {
	return func(key string) string {
		return vals[key]
	}
}

func TestLoadConfigDefaultsWithZeroEnvVars(t *testing.T) {
	cfg, err := loadConfig(fakeGetenv(nil))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.DBPath != "/data/bookmarks.db" {
		t.Errorf("DBPath = %q, want /data/bookmarks.db", cfg.DBPath)
	}
	if cfg.DefaultTheme != "dark" {
		t.Errorf("DefaultTheme = %q, want dark", cfg.DefaultTheme)
	}
	if cfg.AllowPrivateNet != false {
		t.Errorf("AllowPrivateNet = %v, want false", cfg.AllowPrivateNet)
	}
	if cfg.FetchTimeout != 8*time.Second {
		t.Errorf("FetchTimeout = %v, want 8s", cfg.FetchTimeout)
	}
	if cfg.MaxFetchBytes != 1048576 {
		t.Errorf("MaxFetchBytes = %d, want 1048576", cfg.MaxFetchBytes)
	}
	if cfg.MaxFaviconBytes != 65536 {
		t.Errorf("MaxFaviconBytes = %d, want 65536", cfg.MaxFaviconBytes)
	}
	if cfg.MaxImportBytes != 33554432 {
		t.Errorf("MaxImportBytes = %d, want 33554432", cfg.MaxImportBytes)
	}
	if cfg.UserAgent != "Mozilla/5.0 (compatible; bookmarks/1.0)" {
		t.Errorf("UserAgent = %q, want the documented default", cfg.UserAgent)
	}
}

func TestLoadConfigHonoursOverrides(t *testing.T) {
	cfg, err := loadConfig(fakeGetenv(map[string]string{
		"BOOKMARKS_ADDR":              "127.0.0.1:9090",
		"BOOKMARKS_DB":                "/tmp/x.db",
		"BOOKMARKS_THEME":             "light",
		"BOOKMARKS_ALLOW_PRIVATE_NET": "true",
		"BOOKMARKS_FETCH_TIMEOUT":     "3s",
		"BOOKMARKS_MAX_FETCH_BYTES":   "2048",
		"BOOKMARKS_MAX_FAVICON_BYTES": "4096",
		"BOOKMARKS_MAX_IMPORT_BYTES":  "8192",
		"BOOKMARKS_USER_AGENT":        "custom-agent/1.0",
	}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9090" || cfg.DBPath != "/tmp/x.db" || cfg.DefaultTheme != "light" {
		t.Errorf("unexpected cfg: %+v", cfg)
	}
	if !cfg.AllowPrivateNet {
		t.Error("AllowPrivateNet should be true")
	}
	if cfg.FetchTimeout != 3*time.Second {
		t.Errorf("FetchTimeout = %v, want 3s", cfg.FetchTimeout)
	}
	if cfg.MaxFetchBytes != 2048 || cfg.MaxFaviconBytes != 4096 || cfg.MaxImportBytes != 8192 {
		t.Errorf("unexpected byte caps: %+v", cfg)
	}
	if cfg.UserAgent != "custom-agent/1.0" {
		t.Errorf("UserAgent = %q", cfg.UserAgent)
	}
}

func TestLoadConfigInvalidValuesFailFastNamingTheVariable(t *testing.T) {
	cases := []struct {
		key string
		val string
	}{
		{"BOOKMARKS_ALLOW_PRIVATE_NET", "not-a-bool"},
		{"BOOKMARKS_FETCH_TIMEOUT", "not-a-duration"},
		{"BOOKMARKS_MAX_FETCH_BYTES", "not-an-int"},
		{"BOOKMARKS_MAX_FAVICON_BYTES", "not-an-int"},
		{"BOOKMARKS_MAX_IMPORT_BYTES", "not-an-int"},
	}
	for _, c := range cases {
		_, err := loadConfig(fakeGetenv(map[string]string{c.key: c.val}))
		if err == nil {
			t.Errorf("expected an error for invalid %s=%q", c.key, c.val)
			continue
		}
		if !strings.Contains(err.Error(), c.key) {
			t.Errorf("error for %s should name the offending variable, got: %v", c.key, err)
		}
	}
}

func TestRunHealthcheckSuccessAndFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	if code := runHealthcheck(addr); code != 0 {
		t.Errorf("runHealthcheck against a live server = %d, want 0", code)
	}

	if code := runHealthcheck("127.0.0.1:1"); code != 1 {
		t.Errorf("runHealthcheck against a closed port = %d, want 1", code)
	}
}

func TestNewServerAppliesTimeouts(t *testing.T) {
	srv := newServer(Config{Addr: ":0"}, http.NewServeMux())
	if srv.ReadTimeout <= 0 {
		t.Error("expected a positive ReadTimeout")
	}
	if srv.WriteTimeout <= 0 {
		t.Error("expected a positive WriteTimeout")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("expected a positive IdleTimeout")
	}
}

func TestBuildIndexHTMLInjectsTheme(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<html data-theme="{{.DefaultTheme}}"></html>`)},
	}
	out, err := buildIndexHTML(fsys, "dark")
	if err != nil {
		t.Fatalf("buildIndexHTML: %v", err)
	}
	if !strings.Contains(string(out), `data-theme="dark"`) {
		t.Errorf("output = %q, want the theme injected", out)
	}
}

func TestBuildIndexHTMLAgainstEmbeddedPlaceholder(t *testing.T) {
	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	out, err := buildIndexHTML(assets, "light")
	if err != nil {
		t.Fatalf("buildIndexHTML on the embedded placeholder: %v", err)
	}
	if !strings.Contains(string(out), "light") {
		t.Errorf("output = %q, want the theme substituted into the embedded index.html", out)
	}
	if strings.Contains(string(out), "{{.DefaultTheme}}") {
		t.Errorf("template was not executed: %q", out)
	}
}

func TestFullAppWiringHealthz(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bookmarks.db")
	cfg, err := loadConfig(fakeGetenv(map[string]string{"BOOKMARKS_DB": dbPath}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	db, err := openDB(cfg.DBPath)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	indexHTML, err := buildIndexHTML(assets, cfg.DefaultTheme)
	if err != nil {
		t.Fatalf("buildIndexHTML: %v", err)
	}
	app, err := newApp(db, cfg, assets, indexHTML)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}

	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
