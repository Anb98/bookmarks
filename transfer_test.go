package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExportJSONShape(t *testing.T) {
	db := testDB(t)
	if _, _, err := insertBookmark(db, "https://a.example/", "A Title", "A desc", []string{"one", "two"}, 1000); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, _, err := insertBookmark(db, "https://b.example/", "B Title", "", nil, 2000); err != nil {
		t.Fatalf("insert b: %v", err)
	}

	data, err := exportJSON(db, time.Unix(9999, 0))
	if err != nil {
		t.Fatalf("exportJSON: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if v, ok := doc["version"].(float64); !ok || v != 1 {
		t.Errorf("version = %v, want 1", doc["version"])
	}
	if _, ok := doc["exported_at"].(string); !ok {
		t.Errorf("exported_at missing or not a string: %v", doc["exported_at"])
	}
	bms, ok := doc["bookmarks"].([]any)
	if !ok || len(bms) != 2 {
		t.Fatalf("bookmarks = %v, want a 2-element array", doc["bookmarks"])
	}
	first := bms[0].(map[string]any)
	for _, key := range []string{"url", "title", "description", "tags", "created_at"} {
		if _, ok := first[key]; !ok {
			t.Errorf("exported bookmark missing key %q: %v", key, first)
		}
	}
	for _, forbidden := range []string{"has_icon", "domain", "id"} {
		if _, ok := first[forbidden]; ok {
			t.Errorf("exported bookmark should not include %q (favicons/domain/id are excluded)", forbidden)
		}
	}
}

func TestImportJSONRoundTripIsStableNoOp(t *testing.T) {
	db := testDB(t)
	if _, _, err := insertBookmark(db, "https://a.example/", "A Title", "A desc", []string{"one", "two"}, 1000); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, _, err := insertBookmark(db, "https://b.example/", "B Title", "", nil, 2000); err != nil {
		t.Fatalf("insert b: %v", err)
	}

	data, err := exportJSON(db, time.Unix(9999, 0))
	if err != nil {
		t.Fatalf("exportJSON: %v", err)
	}

	res, err := importJSON(db, data, 5000)
	if err != nil {
		t.Fatalf("importJSON: %v", err)
	}
	if res.Created != 0 || res.Updated != 0 || res.Skipped != 2 || len(res.Errors) != 0 {
		t.Errorf("expected a stable no-op import, got %+v", res)
	}
}

func TestImportJSONCreatesThenFillsAndUnionsTags(t *testing.T) {
	db := testDB(t)

	first := `{"version":1,"bookmarks":[{"url":"https://c.example/","title":"","description":"","tags":["one"],"created_at":1000}]}`
	res1, err := importJSON(db, []byte(first), 2000)
	if err != nil {
		t.Fatalf("importJSON first: %v", err)
	}
	if res1.Created != 1 || res1.Updated != 0 || res1.Skipped != 0 {
		t.Fatalf("expected 1 created, got %+v", res1)
	}

	second := `{"version":1,"bookmarks":[{"url":"https://c.example/","title":"Filled Title","description":"Filled Desc","tags":["two"],"created_at":1000}]}`
	res2, err := importJSON(db, []byte(second), 3000)
	if err != nil {
		t.Fatalf("importJSON second: %v", err)
	}
	if res2.Updated != 1 || res2.Created != 0 {
		t.Fatalf("expected 1 updated (title/desc filled, tag unioned), got %+v", res2)
	}

	rows, err := listBookmarks(db, "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(rows))
	}
	bm := rows[0]
	if bm.Title != "Filled Title" || bm.Description != "Filled Desc" {
		t.Errorf("title/description should have been filled: %+v", bm)
	}
	tagSet := map[string]bool{}
	for _, tg := range bm.Tags {
		tagSet[tg] = true
	}
	if !tagSet["one"] || !tagSet["two"] {
		t.Errorf("expected tags unioned to include both 'one' and 'two', got %v", bm.Tags)
	}

	third := `{"version":1,"bookmarks":[{"url":"https://c.example/","title":"Ignored","description":"Ignored","tags":["one"],"created_at":1000}]}`
	res3, err := importJSON(db, []byte(third), 4000)
	if err != nil {
		t.Fatalf("importJSON third: %v", err)
	}
	if res3.Skipped != 1 || res3.Updated != 0 || res3.Created != 0 {
		t.Fatalf("expected a no-op skip once title/desc/tags already present, got %+v", res3)
	}
}

func TestImportJSONInvalidRowDoesNotAbortImport(t *testing.T) {
	db := testDB(t)
	payload := `{"version":1,"bookmarks":[
		{"url":"https://good1.example/","title":"Good1"},
		{"url":"ftp://bad.example/","title":"Bad"},
		{"url":"https://good2.example/","title":"Good2"}
	]}`
	res, err := importJSON(db, []byte(payload), 1000)
	if err != nil {
		t.Fatalf("importJSON: %v", err)
	}
	if res.Created != 2 {
		t.Errorf("expected 2 created despite 1 invalid row, got %+v", res)
	}
	if len(res.Errors) != 1 {
		t.Errorf("expected exactly 1 collected error, got %v", res.Errors)
	}
}

func TestImportJSONErrorsCapAtTwenty(t *testing.T) {
	db := testDB(t)
	var sb strings.Builder
	sb.WriteString(`{"version":1,"bookmarks":[`)
	for i := 0; i < 30; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"url":"not-a-valid-url","title":"x"}`)
	}
	sb.WriteString(`]}`)

	res, err := importJSON(db, []byte(sb.String()), 1000)
	if err != nil {
		t.Fatalf("importJSON: %v", err)
	}
	if len(res.Errors) != 20 {
		t.Errorf("expected errors capped at 20, got %d", len(res.Errors))
	}
	if res.Created != 0 {
		t.Errorf("expected 0 created, got %d", res.Created)
	}
}

func TestImportJSONEmptyFileIsSuccessfulNoOp(t *testing.T) {
	db := testDB(t)
	res, err := importJSON(db, []byte(""), 1000)
	if err != nil {
		t.Fatalf("importJSON on empty file should succeed, got: %v", err)
	}
	if res.Created != 0 || res.Updated != 0 || res.Skipped != 0 || len(res.Errors) != 0 {
		t.Errorf("expected an all-zero result for an empty file, got %+v", res)
	}
}

func TestImportJSONMalformedRejectsWholesale(t *testing.T) {
	db := testDB(t)
	_, err := importJSON(db, []byte("{not valid json"), 1000)
	if err == nil {
		t.Fatal("expected malformed JSON to be rejected")
	}
	rows, err := listBookmarks(db, "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("malformed import must write nothing, found %d rows", len(rows))
	}
}

const netscapeFixture = `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks</H1>
<DL><p>
<DT><H3>Bookmarks bar</H3>
<DL><p>
    <DT><H3>Programming</H3>
    <DL><p>
        <DT><H3>Go</H3>
        <DL><p>
            <DT><A HREF="https://go.dev/" ADD_DATE="1000" TAGS="golang,web">Go Language</A>
            <DD>The Go programming language site.
        </DL><p>
        <DT><A HREF="https://plain.example/">Plain Link</A>
    </DL><p>
</DL><p>
</DL><p>
`

func TestImportNetscapeHTMLFixture(t *testing.T) {
	db := testDB(t)
	res, err := importNetscapeHTML(db, []byte(netscapeFixture), 5000)
	if err != nil {
		t.Fatalf("importNetscapeHTML: %v", err)
	}
	if res.Created != 2 {
		t.Fatalf("expected 2 created rows, got %+v", res)
	}

	rows, err := listBookmarks(db, "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byURL := map[string]Bookmark{}
	for _, bm := range rows {
		byURL[bm.URL] = bm
	}

	godev, ok := byURL["https://go.dev/"]
	if !ok {
		t.Fatal("missing https://go.dev/ bookmark")
	}
	if godev.Title != "Go Language" {
		t.Errorf("title = %q, want %q", godev.Title, "Go Language")
	}
	if godev.Description != "The Go programming language site." {
		t.Errorf("description = %q, want the DD text", godev.Description)
	}
	godevTags := map[string]bool{}
	for _, tg := range godev.Tags {
		godevTags[tg] = true
	}
	for _, want := range []string{"golang", "web", "programming", "go"} {
		if !godevTags[want] {
			t.Errorf("go.dev tags missing %q, got %v", want, godev.Tags)
		}
	}
	if godevTags["bookmarks bar"] {
		t.Errorf("generic folder name 'bookmarks bar' should have been skipped, got tags %v", godev.Tags)
	}

	plain, ok := byURL["https://plain.example/"]
	if !ok {
		t.Fatal("missing https://plain.example/ bookmark")
	}
	if len(plain.Tags) != 1 || plain.Tags[0] != "programming" {
		t.Errorf("plain.example tags = %v, want exactly [programming]", plain.Tags)
	}
}

func TestImportNetscapeHTMLMalformedRejectsWholesale(t *testing.T) {
	db := testDB(t)
	invalidUTF8 := []byte{0xff, 0xfe, 0xfd, 0x00, 0x01}
	_, err := importNetscapeHTML(db, invalidUTF8, 1000)
	if err == nil {
		t.Fatal("expected invalid-UTF-8 payload to be rejected as malformed")
	}
	rows, err := listBookmarks(db, "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("malformed netscape import must write nothing, found %d rows", len(rows))
	}
}

func TestImportNetscapeHTMLEmptyFileIsSuccessfulNoOp(t *testing.T) {
	db := testDB(t)
	res, err := importNetscapeHTML(db, []byte(""), 1000)
	if err != nil {
		t.Fatalf("importNetscapeHTML on empty file should succeed, got: %v", err)
	}
	if res.Created != 0 || res.Updated != 0 || res.Skipped != 0 || len(res.Errors) != 0 {
		t.Errorf("expected an all-zero result for an empty file, got %+v", res)
	}
}

func TestImportMakesZeroOutboundRequests(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	db := testDB(t)
	var sb strings.Builder
	sb.WriteString(`{"version":1,"bookmarks":[`)
	for i := 0; i < 50; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"url":"` + server.URL + "/page/" + itoaTest(i) + `","title":"Page"}`)
	}
	sb.WriteString(`]}`)

	res, err := importJSON(db, []byte(sb.String()), 1000)
	if err != nil {
		t.Fatalf("importJSON: %v", err)
	}
	if res.Created != 50 {
		t.Fatalf("expected 50 created rows, got %+v", res)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("import must make zero outbound requests, but the server saw %d", got)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
