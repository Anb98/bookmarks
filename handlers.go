package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config holds every runtime-tunable setting; see main.go for env parsing
// and defaults.
type Config struct {
	Addr            string
	DBPath          string
	DefaultTheme    string
	AllowPrivateNet bool
	FetchTimeout    time.Duration
	MaxFetchBytes   int64
	MaxFaviconBytes int64
	MaxImportBytes  int64
	UserAgent       string
}

// App wires the database, configuration, and static assets into HTTP routes.
type App struct {
	db           *sql.DB
	cfg          Config
	indexHTML    []byte
	assetETags   map[string]string
	assetHandler http.Handler
}

func newApp(db *sql.DB, cfg Config, assets fs.FS, indexHTML []byte) (*App, error) {
	etags, err := buildAssetETags(assets)
	if err != nil {
		return nil, err
	}
	return &App{
		db:           db,
		cfg:          cfg,
		indexHTML:    indexHTML,
		assetETags:   etags,
		assetHandler: http.StripPrefix("/assets/", http.FileServerFS(assets)),
	}, nil
}

func buildAssetETags(assets fs.FS) (map[string]string, error) {
	etags := map[string]string{}
	err := fs.WalkDir(assets, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		etags[path] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return etags, nil
}

// Routes builds the full mux. Every error response body is {"error": "..."}.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /assets/{path...}", a.handleAssets)
	mux.HandleFunc("GET /api/bookmarks", a.handleListBookmarks)
	mux.HandleFunc("POST /api/bookmarks", a.handleCreateBookmark)
	mux.HandleFunc("PUT /api/bookmarks/{id}", a.handleUpdateBookmark)
	mux.HandleFunc("DELETE /api/bookmarks/{id}", a.handleDeleteBookmark)
	mux.HandleFunc("POST /api/bookmarks/{id}/visit", a.handleRecordVisit)
	mux.HandleFunc("POST /api/metadata", a.handleMetadata)
	mux.HandleFunc("GET /api/favicon/{domain}", a.handleFavicon)
	mux.HandleFunc("GET /api/export", a.handleExport)
	mux.HandleFunc("POST /api/import", a.handleImport)
	mux.HandleFunc("GET /healthz", a.handleHealthz)
	return mux
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func setHTMLSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	setHTMLSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(a.indexHTML)
}

func (a *App) handleAssets(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	if etag, ok := a.assetETags[p]; ok {
		w.Header().Set("ETag", `"`+etag+`"`)
	}
	if strings.HasPrefix(p, "fonts/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	a.assetHandler.ServeHTTP(w, r)
}

func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

type listEnvelope struct {
	Bookmarks      []Bookmark `json:"bookmarks"`
	Tags           []TagCount `json:"tags"`
	Matched        int        `json:"matched"`
	TotalBookmarks int        `json:"total_bookmarks"`
	TotalTags      int        `json:"total_tags"`
}

func (a *App) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var tags []string
	if raw := r.URL.Query().Get("tags"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			norm := normalizeTag(t)
			if norm == "" {
				writeError(w, http.StatusBadRequest, "malformed tag")
				return
			}
			tags = append(tags, norm)
		}
	}

	bms, err := listBookmarks(a.db, q, tags, r.URL.Query().Get("sort"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	allTags, err := tagCounts(a.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	total, err := countBookmarks(a.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, listEnvelope{
		Bookmarks:      bms,
		Tags:           allTags,
		Matched:        len(bms),
		TotalBookmarks: total,
		TotalTags:      len(allTags),
	})
}

// handleRecordVisit backs the "most used" sort. It answers 204 for anything
// that parses, including an id that no longer exists: the client is a
// fire-and-forget beacon sent while the browser opens the bookmark in another
// tab, so it cannot read a status or retry.
func (a *App) handleRecordVisit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := recordVisit(a.db, id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type bookmarkRequest struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

func (a *App) handleCreateBookmark(w http.ResponseWriter, r *http.Request) {
	var body bookmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	normURL, err := normalizeURL(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid url")
		return
	}

	id, existed, err := insertBookmark(a.db, normURL, body.Title, body.Description, body.Tags, time.Now().Unix())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if existed {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already saved", "id": id})
		return
	}

	bm, ok, err := getBookmark(a.db, id)
	if err != nil || !ok {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, bm)
}

func (a *App) handleUpdateBookmark(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body bookmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	normURL, err := normalizeURL(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid url")
		return
	}

	found, conflict, err := updateBookmark(a.db, id, normURL, body.Title, body.Description, body.Tags, time.Now().Unix())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if conflict {
		writeError(w, http.StatusConflict, "already saved")
		return
	}

	bm, ok, err := getBookmark(a.db, id)
	if err != nil || !ok {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, bm)
}

func (a *App) handleDeleteBookmark(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	found, err := deleteBookmark(a.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// allowedFaviconMIMEs is the strict allowlist that decides the stored and
// served Content-Type; the remote server's own Content-Type header is never
// trusted for this.
var allowedFaviconMIMEs = map[string]bool{
	"image/png":     true,
	"image/jpeg":    true,
	"image/gif":     true,
	"image/webp":    true,
	"image/x-icon":  true,
	"image/svg+xml": true,
	"image/bmp":     true, // some old sites serve a raw BMP as favicon.ico
}

// detectFaviconMIME sniffs real image bytes only. SVG has no dedicated sniff
// signature in http.DetectContentType, so an XML/HTML/text sniff result gets
// one extra check for a literal "<svg" root element before being accepted.
func detectFaviconMIME(data []byte) string {
	sniffed := http.DetectContentType(data)
	base, _, err := mime.ParseMediaType(sniffed)
	if err != nil {
		base = sniffed
	}
	if allowedFaviconMIMEs[base] {
		return base
	}
	if strings.Contains(base, "xml") || strings.Contains(base, "html") || strings.HasPrefix(base, "text/plain") {
		head := data
		if len(head) > 512 {
			head = head[:512]
		}
		if strings.Contains(strings.ToLower(string(head)), "<svg") {
			return "image/svg+xml"
		}
	}
	return ""
}

func resolveURL(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}

type metadataResponse struct {
	URL         string `json:"url"`
	Domain      string `json:"domain"`
	Title       string `json:"title"`
	Description string `json:"description"`
	HasIcon     bool   `json:"has_icon"`
	FetchError  string `json:"fetch_error"`
}

// handleMetadata always answers 200 with a fetch_error field on any fetch
// failure (SSRF refusal, timeout, unreachable host, non-2xx, oversized
// body): the spec requires that a fetch failure never blocks saving, and a
// 200-with-a-field makes that structurally impossible for the frontend to
// get wrong. Only a structurally malformed URL is a 400.
//
// Favicon placeholder policy: only the page's own declared <link rel=icon>
// or its own /favicon.ico is ever fetched (no third-party favicon services),
// and no favicons row is written unless that fetch yields a non-empty,
// real, allowlisted-MIME image. A soft-404 HTML page, a non-2xx status, or
// an empty body must leave the domain iconless so the frontend's monogram
// fallback renders instead of a fake placeholder.
func (a *App) handleMetadata(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	normURL, err := normalizeURL(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid url")
		return
	}

	resp := metadataResponse{URL: normURL, Domain: domainOf(normURL)}

	pageCfg := fetchConfig{
		allowPrivate: a.cfg.AllowPrivateNet,
		timeout:      a.cfg.FetchTimeout,
		maxBytes:     a.cfg.MaxFetchBytes,
		userAgent:    a.cfg.UserAgent,
	}
	var md Metadata
	baseURL := normURL
	pageBytes, finalURL, err := fetchURL(r.Context(), pageCfg, normURL)
	if err == nil {
		baseURL = finalURL
		md, err = extractMetadata(bytes.NewReader(pageBytes))
	}
	if err != nil {
		resp.FetchError = err.Error()
		// Anti-bot walls refuse the page but usually still serve /favicon.ico,
		// so keep going; only a timed-out host is skipped, since it would just
		// time out a second time on the favicon.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}
	resp.Title = md.Title
	resp.Description = md.Description

	iconHref := md.IconHref
	if iconHref == "" {
		iconHref = "/favicon.ico"
	}
	if iconURL, err := resolveURL(baseURL, iconHref); err == nil {
		faviconCfg := fetchConfig{
			allowPrivate: a.cfg.AllowPrivateNet,
			timeout:      a.cfg.FetchTimeout,
			maxBytes:     a.cfg.MaxFaviconBytes,
			userAgent:    a.cfg.UserAgent,
		}
		if iconBytes, _, ferr := fetchURL(r.Context(), faviconCfg, iconURL); ferr == nil && len(iconBytes) > 0 {
			if mimeType := detectFaviconMIME(iconBytes); mimeType != "" {
				if err := putFavicon(a.db, resp.Domain, mimeType, iconBytes, time.Now().Unix()); err == nil {
					resp.HasIcon = true
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (a *App) handleFavicon(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	fv, ok, err := getFavicon(a.db, domain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	etag := `"` + fv.ETag + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")

	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", fv.MIME)
	w.Write(fv.Bytes)
}

func (a *App) handleExport(w http.ResponseWriter, r *http.Request) {
	data, err := exportJSON(a.db, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="bookmarks-export.json"`)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (a *App) handleImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxImportBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "import payload too large")
			return
		}
		writeError(w, http.StatusBadRequest, "unable to read request body")
		return
	}

	now := time.Now().Unix()
	trimmed := strings.TrimSpace(string(data))
	ct := r.Header.Get("Content-Type")

	var res ImportResult
	switch {
	case trimmed == "":
		res, err = importJSON(a.db, data, now)
	case strings.Contains(ct, "html"), strings.HasPrefix(trimmed, "<"):
		res, err = importNetscapeHTML(a.db, data, now)
	default:
		res, err = importJSON(a.db, data, now)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "unrecognized import file")
		return
	}
	writeJSON(w, http.StatusOK, res)
}
