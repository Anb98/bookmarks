package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testApp(t *testing.T, maxImportBytes int64) (*App, *sql.DB) {
	t.Helper()
	return testAppWithConfig(t, Config{
		AllowPrivateNet: false,
		FetchTimeout:    2 * time.Second,
		MaxFetchBytes:   1024 * 1024,
		MaxFaviconBytes: 64 * 1024,
		MaxImportBytes:  maxImportBytes,
		UserAgent:       "test-agent",
	})
}

// testAppAllowPrivate permits loopback fetches, needed only to exercise the
// real fetch/metadata/favicon flow against an httptest server.
func testAppAllowPrivate(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	return testAppWithConfig(t, Config{
		AllowPrivateNet: true,
		FetchTimeout:    2 * time.Second,
		MaxFetchBytes:   1024 * 1024,
		MaxFaviconBytes: 64 * 1024,
		MaxImportBytes:  1 << 20,
		UserAgent:       "test-agent",
	})
}

func testAppWithConfig(t *testing.T, cfg Config) (*App, *sql.DB) {
	t.Helper()
	db := testDB(t)
	assets := fstest.MapFS{
		"app.css":         &fstest.MapFile{Data: []byte("body{color:red}")},
		"fonts/foo.woff2": &fstest.MapFile{Data: []byte("fake-font-bytes")},
	}
	app, err := newApp(db, cfg, assets, []byte("<html>index</html>"))
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return app, db
}

func decodeJSON(t *testing.T, r *http.Response, v any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("decode json: %v", err)
	}
}

func TestHandlerHealthz(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestHandlerIndex(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp == "" {
		t.Error("expected a Content-Security-Policy header on the index response")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("expected X-Content-Type-Options: nosniff on the index response")
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<html>index</html>" {
		t.Errorf("body = %q, want the rendered index bytes", body)
	}
}

func TestHandlerAssetTraversalReturns404(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp, err := http.Get(server.URL + "/assets/../../etc/passwd")
	if err != nil {
		t.Fatalf("GET traversal: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandlerAssetServedWithETagAndCacheControl(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp, err := http.Get(server.URL + "/assets/app.css")
	if err != nil {
		t.Fatalf("GET /assets/app.css: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("expected an ETag header for a served asset")
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache for css", resp.Header.Get("Cache-Control"))
	}

	respFont, err := http.Get(server.URL + "/assets/fonts/foo.woff2")
	if err != nil {
		t.Fatalf("GET /assets/fonts/foo.woff2: %v", err)
	}
	defer respFont.Body.Close()
	if got := respFont.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("font Cache-Control = %q, want immutable long cache", got)
	}
}

func TestHandlerCreateThenDuplicateReturns409(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	body := `{"url":"https://example.com/a","title":"Example","description":"d","tags":["go"]}`
	resp, err := http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created map[string]any
	decodeJSON(t, resp, &created)
	if created["id"] == nil {
		t.Fatalf("created response missing id: %v", created)
	}

	resp2, err := http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST duplicate: %v", err)
	}
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp2.StatusCode)
	}
	var conflict map[string]any
	decodeJSON(t, resp2, &conflict)
	if conflict["error"] != "already saved" {
		t.Errorf("conflict error = %v, want %q", conflict["error"], "already saved")
	}
	if conflict["id"] == nil {
		t.Errorf("conflict response missing id: %v", conflict)
	}
}

func TestHandlerCreateInvalidURLReturns400(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(`{"url":"ftp://bad.example/"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlerDeleteAndNotFound(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(`{"url":"https://del.example/"}`))
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	var created map[string]any
	decodeJSON(t, resp, &created)
	id := int64(created["id"].(float64))

	req, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/bookmarks/"+strconv.FormatInt(id, 10), nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", delResp.StatusCode)
	}

	req2, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/bookmarks/"+strconv.FormatInt(id, 10), nil)
	delResp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("DELETE again: %v", err)
	}
	if delResp2.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", delResp2.StatusCode)
	}
}

func TestHandlerUpdateFullReplaceAndConflict(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp1, _ := http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(`{"url":"https://one.example/","title":"One"}`))
	var b1 map[string]any
	decodeJSON(t, resp1, &b1)
	id1 := int64(b1["id"].(float64))

	resp2, _ := http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(`{"url":"https://two.example/","title":"Two"}`))
	var b2 map[string]any
	decodeJSON(t, resp2, &b2)
	id2 := int64(b2["id"].(float64))

	putReq, _ := http.NewRequest(http.MethodPut, server.URL+"/api/bookmarks/"+strconv.FormatInt(id1, 10),
		strings.NewReader(`{"url":"https://one-updated.example/","title":"One Updated","description":"","tags":["x"]}`))
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", putResp.StatusCode)
	}

	conflictReq, _ := http.NewRequest(http.MethodPut, server.URL+"/api/bookmarks/"+strconv.FormatInt(id2, 10),
		strings.NewReader(`{"url":"https://one-updated.example/","title":"Two"}`))
	conflictReq.Header.Set("Content-Type", "application/json")
	conflictResp, err := http.DefaultClient.Do(conflictReq)
	if err != nil {
		t.Fatalf("PUT conflict: %v", err)
	}
	if conflictResp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", conflictResp.StatusCode)
	}

	notFoundReq, _ := http.NewRequest(http.MethodPut, server.URL+"/api/bookmarks/999999",
		strings.NewReader(`{"url":"https://none.example/"}`))
	notFoundReq.Header.Set("Content-Type", "application/json")
	notFoundResp, err := http.DefaultClient.Do(notFoundReq)
	if err != nil {
		t.Fatalf("PUT missing: %v", err)
	}
	if notFoundResp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", notFoundResp.StatusCode)
	}
}

func TestHandlerListBookmarksEnvelopeAndMalformedTag(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(`{"url":"https://list1.example/","title":"One","tags":["go","web"]}`))
	http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(`{"url":"https://list2.example/","title":"Two"}`))

	resp, err := http.Get(server.URL + "/api/bookmarks")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	var envelope struct {
		Bookmarks      []map[string]any `json:"bookmarks"`
		Tags           []map[string]any `json:"tags"`
		Matched        int              `json:"matched"`
		TotalBookmarks int              `json:"total_bookmarks"`
		TotalTags      int              `json:"total_tags"`
	}
	decodeJSON(t, resp, &envelope)
	if envelope.TotalBookmarks != 2 {
		t.Errorf("total_bookmarks = %d, want 2", envelope.TotalBookmarks)
	}
	if envelope.Matched != 2 {
		t.Errorf("matched = %d, want 2", envelope.Matched)
	}
	if envelope.TotalTags != 2 {
		t.Errorf("total_tags = %d, want 2", envelope.TotalTags)
	}

	badResp, err := http.Get(server.URL + "/api/bookmarks?tags=" + "%00")
	if err != nil {
		t.Fatalf("GET malformed tag: %v", err)
	}
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a malformed tag", badResp.StatusCode)
	}
}

func TestHandlerFaviconNotFoundThenFoundThen304(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	missResp, err := http.Get(server.URL + "/api/favicon/missing.example")
	if err != nil {
		t.Fatalf("GET missing favicon: %v", err)
	}
	if missResp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", missResp.StatusCode)
	}

	if err := putFavicon(app.db, "icon.example", "image/png", []byte("fake-png-bytes"), time.Now().Unix()); err != nil {
		t.Fatalf("putFavicon: %v", err)
	}

	resp, err := http.Get(server.URL + "/api/favicon/icon.example")
	if err != nil {
		t.Fatalf("GET favicon: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected an ETag header")
	}
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("expected X-Content-Type-Options: nosniff on favicon response")
	}

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/favicon/icon.example", nil)
	req.Header.Set("If-None-Match", etag)
	notModResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with If-None-Match: %v", err)
	}
	if notModResp.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", notModResp.StatusCode)
	}
}

func TestHandlerExportIsJSONAttachment(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	http.Post(server.URL+"/api/bookmarks", "application/json", strings.NewReader(`{"url":"https://export.example/","title":"Export Me"}`))

	resp, err := http.Get(server.URL + "/api/export")
	if err != nil {
		t.Fatalf("GET export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", resp.Header.Get("Content-Disposition"))
	}
	var doc map[string]any
	decodeJSON(t, resp, &doc)
	if doc["version"].(float64) != 1 {
		t.Errorf("version = %v, want 1", doc["version"])
	}
}

func TestHandlerImportJSONSuccess(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	payload := `{"version":1,"bookmarks":[{"url":"https://imported.example/","title":"Imported"}]}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/import", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var res ImportResult
	decodeJSON(t, resp, &res)
	if res.Created != 1 {
		t.Errorf("created = %d, want 1", res.Created)
	}
}

func TestHandlerImportOverCapReturns413(t *testing.T) {
	app, _ := testApp(t, 100)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	big := `{"version":1,"bookmarks":[{"url":"https://big.example/","title":"` + strings.Repeat("x", 500) + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/import", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestHandlerImportUnrecognizedReturns400(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/import", strings.NewReader("{not valid json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlerMetadataSSRFRefusalReturns200WithFetchError(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	loopback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><head><title>Should not be reached</title></head></html>"))
	}))
	defer loopback.Close()

	payload := `{"url":"` + loopback.URL + `"}`
	resp, err := http.Post(server.URL+"/api/metadata", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST metadata: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when the fetch is SSRF-refused", resp.StatusCode)
	}
	var out struct {
		URL        string `json:"url"`
		Domain     string `json:"domain"`
		Title      string `json:"title"`
		HasIcon    bool   `json:"has_icon"`
		FetchError string `json:"fetch_error"`
	}
	decodeJSON(t, resp, &out)
	if out.FetchError == "" {
		t.Error("expected fetch_error to be populated for a refused loopback fetch")
	}
	if out.Title != "" {
		t.Errorf("title should be empty on fetch failure, got %q", out.Title)
	}
}

// TestHandlerMetadataFaviconSoftHTMLPageStoresNothing locks in the favicon
// placeholder policy: a favicon.ico URL that returns an HTML page with a 200
// status (an extremely common "soft 404") must never be stored, so the
// frontend's monogram fallback renders instead of a fake placeholder icon.
func TestHandlerMetadataFaviconSoftHTMLPageStoresNothing(t *testing.T) {
	app, db := testAppAllowPrivate(t)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><head><title>Target Page</title></head><body>Not Found</body></html>"))
	}))
	defer target.Close()

	payload := `{"url":"` + target.URL + `"}`
	resp, err := http.Post(server.URL+"/api/metadata", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST metadata: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		HasIcon bool `json:"has_icon"`
	}
	decodeJSON(t, resp, &out)
	if out.HasIcon {
		t.Error("has_icon should be false when favicon.ico is a soft-404 HTML page")
	}

	_, ok, err := getFavicon(db, domainOf(target.URL))
	if err != nil {
		t.Fatalf("getFavicon: %v", err)
	}
	if ok {
		t.Error("expected zero favicons rows for a soft-404 HTML favicon.ico response")
	}
}

// TestHandlerMetadataBlockedPageStillStoresFavicon covers anti-bot sites
// (WAF challenges, login walls) that refuse the page but serve /favicon.ico.
func TestHandlerMetadataBlockedPageStillStoresFavicon(t *testing.T) {
	app, db := testAppAllowPrivate(t)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	png := []byte("\x89PNG\r\n\x1a\nrest-of-image")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.Write(png)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer target.Close()

	payload := `{"url":"` + target.URL + `/page"}`
	resp, err := http.Post(server.URL+"/api/metadata", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST metadata: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Title      string `json:"title"`
		HasIcon    bool   `json:"has_icon"`
		FetchError string `json:"fetch_error"`
	}
	decodeJSON(t, resp, &out)
	if out.FetchError == "" {
		t.Error("expected fetch_error for a 403 page")
	}
	if out.Title != "" {
		t.Errorf("title should be empty on fetch failure, got %q", out.Title)
	}
	if !out.HasIcon {
		t.Error("has_icon should be true when /favicon.ico is a real image")
	}
	fv, ok, err := getFavicon(db, domainOf(target.URL))
	if err != nil || !ok {
		t.Fatalf("getFavicon: ok=%v err=%v, want a stored row", ok, err)
	}
	if fv.MIME != "image/png" {
		t.Errorf("stored MIME = %q, want image/png", fv.MIME)
	}
}

// postMetadataIcon posts url to /api/metadata and returns has_icon plus the
// stored favicon MIME ("" when no row was written).
func postMetadataIcon(t *testing.T, serverURL, url string, db *sql.DB) (bool, string) {
	t.Helper()
	resp, err := http.Post(serverURL+"/api/metadata", "application/json", strings.NewReader(`{"url":"`+url+`"}`))
	if err != nil {
		t.Fatalf("POST metadata: %v", err)
	}
	var out struct {
		HasIcon bool `json:"has_icon"`
	}
	decodeJSON(t, resp, &out)
	fv, ok, err := getFavicon(db, domainOf(url))
	if err != nil {
		t.Fatalf("getFavicon: %v", err)
	}
	if !ok {
		return out.HasIcon, ""
	}
	return out.HasIcon, fv.MIME
}

// TestHandlerMetadataRelativeIconResolvesAgainstRedirectedURL covers pages
// like /app that redirect to /app/ and declare ./icon.png: the icon lives
// under /app/, not at the root.
func TestHandlerMetadataRelativeIconResolvesAgainstRedirectedURL(t *testing.T) {
	app, db := testAppAllowPrivate(t)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app":
			http.Redirect(w, r, "/app/", http.StatusMovedPermanently)
		case "/app/":
			w.Write([]byte(`<html><head><title>App</title><link rel="icon" href="./icon.png"></head></html>`))
		case "/app/icon.png":
			w.Write([]byte("\x89PNG\r\n\x1a\nrest-of-image"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	hasIcon, mimeType := postMetadataIcon(t, server.URL, target.URL+"/app", db)
	if !hasIcon || mimeType != "image/png" {
		t.Errorf("has_icon=%v stored MIME=%q, want true and image/png", hasIcon, mimeType)
	}
}

func TestHandlerMetadataBMPFaviconIsStored(t *testing.T) {
	app, db := testAppAllowPrivate(t)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.Write([]byte("BM6\x03\x00\x00\x00\x00\x00\x006\x00\x00\x00"))
			return
		}
		w.Write([]byte(`<html><head><title>No declared icon</title></head></html>`))
	}))
	defer target.Close()

	hasIcon, mimeType := postMetadataIcon(t, server.URL, target.URL+"/", db)
	if !hasIcon || mimeType != "image/bmp" {
		t.Errorf("has_icon=%v stored MIME=%q, want true and image/bmp", hasIcon, mimeType)
	}
}

func TestHandlerMetadataMalformedURLReturns400(t *testing.T) {
	app, _ := testApp(t, 1<<20)
	server := httptest.NewServer(app.Routes())
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/metadata", "application/json", strings.NewReader(`{"url":"ftp://bad.example/"}`))
	if err != nil {
		t.Fatalf("POST metadata: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a structurally invalid url", resp.StatusCode)
	}
}
