package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bookmarks.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func tableNames(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[name] = true
	}
	return out
}

func indexNames(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index'`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[name] = true
	}
	return out
}

func TestOpenDBCreatesSchema(t *testing.T) {
	db := testDB(t)

	tables := tableNames(t, db)
	for _, want := range []string{"bookmarks", "tags", "bookmark_tags", "favicons"} {
		if !tables[want] {
			t.Errorf("missing table %q, got tables %v", want, tables)
		}
	}

	indices := indexNames(t, db)
	for _, want := range []string{"bookmarks_created_idx", "bookmarks_domain_idx", "bookmark_tags_tag_idx"} {
		if !indices[want] {
			t.Errorf("missing index %q, got indices %v", want, indices)
		}
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1", version)
	}
}

func TestOpenDBRejectsNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bookmarks.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	db.Close()

	_, err = openDB(path)
	if err == nil {
		t.Fatal("openDB with a future schema version should fail, got nil error")
	}
}

func TestInsertBookmarkDuplicateURL(t *testing.T) {
	db := testDB(t)

	id1, existed1, err := insertBookmark(db, "https://example.com/", "Example", "desc", []string{"go", "web"}, 1000)
	if err != nil {
		t.Fatalf("insertBookmark: %v", err)
	}
	if existed1 {
		t.Fatal("first insert should not report existed=true")
	}

	id2, existed2, err := insertBookmark(db, "https://example.com/", "Example Again", "desc2", nil, 2000)
	if err != nil {
		t.Fatalf("insertBookmark second: %v", err)
	}
	if !existed2 {
		t.Fatal("duplicate insert should report existed=true")
	}
	if id1 != id2 {
		t.Fatalf("duplicate insert returned different id: %d vs %d", id1, id2)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE url = ?`, "https://example.com/").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one row, got %d", count)
	}
}

func TestListBookmarksTagFiltering(t *testing.T) {
	db := testDB(t)

	idTagged, _, err := insertBookmark(db, "https://a.example/", "A", "", []string{"go", "web"}, 1000)
	if err != nil {
		t.Fatalf("insert a: %v", err)
	}
	idPartial, _, err := insertBookmark(db, "https://b.example/", "B", "", []string{"go"}, 2000)
	if err != nil {
		t.Fatalf("insert b: %v", err)
	}
	idUntagged, _, err := insertBookmark(db, "https://c.example/", "C searchable text", "", nil, 3000)
	if err != nil {
		t.Fatalf("insert c: %v", err)
	}

	all, err := listBookmarks(db, "", nil)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 bookmarks with zero tags selected, got %d", len(all))
	}
	foundUntagged := false
	for _, bm := range all {
		if bm.ID == idUntagged {
			foundUntagged = true
			if len(bm.Tags) != 0 {
				t.Errorf("untagged bookmark should have no tags, got %v", bm.Tags)
			}
		}
	}
	if !foundUntagged {
		t.Error("untagged bookmark missing from unfiltered list")
	}

	oneTag, err := listBookmarks(db, "", []string{"web"})
	if err != nil {
		t.Fatalf("list one tag: %v", err)
	}
	if len(oneTag) != 1 || oneTag[0].ID != idTagged {
		t.Fatalf("filtering by 'web' should return only %d, got %v", idTagged, oneTag)
	}

	twoTags, err := listBookmarks(db, "", []string{"go", "web"})
	if err != nil {
		t.Fatalf("list two tags: %v", err)
	}
	if len(twoTags) != 1 || twoTags[0].ID != idTagged {
		t.Fatalf("AND filtering by 'go'+'web' should return only %d (HAVING boundary), got %v", idTagged, twoTags)
	}
	if idPartial == 0 {
		t.Fatal("idPartial unused sanity check")
	}

	threeTags, err := listBookmarks(db, "", []string{"go", "web", "nonexistent"})
	if err != nil {
		t.Fatalf("list three tags: %v", err)
	}
	if len(threeTags) != 0 {
		t.Fatalf("filtering by a tag nobody has should return zero rows, got %v", threeTags)
	}

	combined, err := listBookmarks(db, "a.example", []string{"go", "web"})
	if err != nil {
		t.Fatalf("list combined: %v", err)
	}
	if len(combined) != 1 || combined[0].ID != idTagged {
		t.Fatalf("tag+query combined should return only %d, got %v", idTagged, combined)
	}

	noMatch, err := listBookmarks(db, "b.example", []string{"web"})
	if err != nil {
		t.Fatalf("list no match: %v", err)
	}
	if len(noMatch) != 0 {
		t.Fatalf("query 'b.example' with tag 'web' should exclude b.example (it lacks 'web'), got %v", noMatch)
	}
}

func TestUpdateBookmark(t *testing.T) {
	db := testDB(t)

	id, _, err := insertBookmark(db, "https://old.example/", "Old", "old desc", []string{"one", "two"}, 1000)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	found, conflict, err := updateBookmark(db, id, "https://new.example/", "New", "new desc", []string{"three"}, 2000)
	if err != nil {
		t.Fatalf("updateBookmark: %v", err)
	}
	if !found || conflict {
		t.Fatalf("expected found=true conflict=false, got found=%v conflict=%v", found, conflict)
	}

	rows, err := listBookmarks(db, "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(rows))
	}
	bm := rows[0]
	if bm.URL != "https://new.example/" || bm.Title != "New" || bm.Description != "new desc" {
		t.Errorf("update did not replace fields: %+v", bm)
	}
	if len(bm.Tags) != 1 || bm.Tags[0] != "three" {
		t.Errorf("expected tags [three], got %v", bm.Tags)
	}

	var orphanCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tags WHERE name IN ('one','two')`).Scan(&orphanCount); err != nil {
		t.Fatalf("count orphan tags: %v", err)
	}
	if orphanCount != 0 {
		t.Errorf("expected orphan tags 'one' and 'two' to be swept, found %d remaining", orphanCount)
	}

	found404, _, err := updateBookmark(db, id+999, "https://none.example/", "x", "y", nil, 3000)
	if err != nil {
		t.Fatalf("updateBookmark missing id: %v", err)
	}
	if found404 {
		t.Error("updating a missing id should report found=false")
	}
}

func TestUpdateBookmarkURLConflict(t *testing.T) {
	db := testDB(t)

	_, _, err := insertBookmark(db, "https://taken.example/", "Taken", "", nil, 1000)
	if err != nil {
		t.Fatalf("insert taken: %v", err)
	}
	id2, _, err := insertBookmark(db, "https://mine.example/", "Mine", "", nil, 2000)
	if err != nil {
		t.Fatalf("insert mine: %v", err)
	}

	found, conflict, err := updateBookmark(db, id2, "https://taken.example/", "Mine", "", nil, 3000)
	if err != nil {
		t.Fatalf("updateBookmark: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if !conflict {
		t.Fatal("expected conflict=true when updating to another bookmark's url")
	}
}

func TestDeleteBookmarkCascade(t *testing.T) {
	db := testDB(t)

	id, _, err := insertBookmark(db, "https://gone.example/", "Gone", "", []string{"solo"}, 1000)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	found, err := deleteBookmark(db, id)
	if err != nil {
		t.Fatalf("deleteBookmark: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for existing id")
	}

	var linkCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bookmark_tags WHERE bookmark_id = ?`, id).Scan(&linkCount); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if linkCount != 0 {
		t.Errorf("expected zero orphan bookmark_tags rows, got %d", linkCount)
	}

	var tagCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tags WHERE name = 'solo'`).Scan(&tagCount); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if tagCount != 0 {
		t.Errorf("expected orphan tag 'solo' swept, still present")
	}

	found2, err := deleteBookmark(db, id)
	if err != nil {
		t.Fatalf("deleteBookmark again: %v", err)
	}
	if found2 {
		t.Error("deleting an already-deleted id should report found=false")
	}
}

func TestSweepOrphanFaviconsByDomain(t *testing.T) {
	db := testDB(t)

	id, _, err := insertBookmark(db, "https://icon.example/", "Icon", "", nil, 1000)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := putFavicon(db, "icon.example", "image/png", []byte("fake-bytes"), 1000); err != nil {
		t.Fatalf("putFavicon: %v", err)
	}

	if _, err := deleteBookmark(db, id); err != nil {
		t.Fatalf("deleteBookmark: %v", err)
	}

	_, ok, err := getFavicon(db, "icon.example")
	if err != nil {
		t.Fatalf("getFavicon: %v", err)
	}
	if ok {
		t.Error("expected favicon for orphaned domain to be swept after bookmark deletion")
	}
}

func TestGetPutFavicon(t *testing.T) {
	db := testDB(t)

	data := []byte("\x89PNG-fake-icon-bytes")
	if err := putFavicon(db, "site.example", "image/png", data, 5000); err != nil {
		t.Fatalf("putFavicon: %v", err)
	}

	fv, ok, err := getFavicon(db, "site.example")
	if err != nil {
		t.Fatalf("getFavicon: %v", err)
	}
	if !ok {
		t.Fatal("expected favicon to be found")
	}
	wantEtag := fmt.Sprintf("%x", sha256.Sum256(data))
	if fv.ETag != wantEtag {
		t.Errorf("etag = %q, want %q", fv.ETag, wantEtag)
	}
	if string(fv.Bytes) != string(data) {
		t.Errorf("bytes mismatch")
	}
	if fv.MIME != "image/png" {
		t.Errorf("mime = %q, want image/png", fv.MIME)
	}

	newData := []byte("second-version")
	if err := putFavicon(db, "site.example", "image/webp", newData, 6000); err != nil {
		t.Fatalf("putFavicon update: %v", err)
	}
	fv2, ok, err := getFavicon(db, "site.example")
	if err != nil || !ok {
		t.Fatalf("getFavicon after update: ok=%v err=%v", ok, err)
	}
	if string(fv2.Bytes) != string(newData) || fv2.MIME != "image/webp" {
		t.Errorf("upsert did not replace favicon: %+v", fv2)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM favicons WHERE domain = 'site.example'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one favicon row after upsert, got %d", count)
	}

	_, ok, err = getFavicon(db, "missing.example")
	if err != nil {
		t.Fatalf("getFavicon missing: %v", err)
	}
	if ok {
		t.Error("expected ok=false for a domain with no favicon")
	}
}

func TestTagCountsGlobalUnfiltered(t *testing.T) {
	db := testDB(t)

	if _, _, err := insertBookmark(db, "https://x1.example/", "X1", "", []string{"go", "web"}, 1000); err != nil {
		t.Fatalf("insert x1: %v", err)
	}
	if _, _, err := insertBookmark(db, "https://x2.example/", "X2", "", []string{"go"}, 2000); err != nil {
		t.Fatalf("insert x2: %v", err)
	}

	counts, err := tagCounts(db)
	if err != nil {
		t.Fatalf("tagCounts: %v", err)
	}
	got := map[string]int{}
	for _, tc := range counts {
		got[tc.Name] = tc.Count
	}
	if got["go"] != 2 {
		t.Errorf("go count = %d, want 2", got["go"])
	}
	if got["web"] != 1 {
		t.Errorf("web count = %d, want 1", got["web"])
	}
}

func TestCountBookmarks(t *testing.T) {
	db := testDB(t)
	n, err := countBookmarks(db)
	if err != nil {
		t.Fatalf("countBookmarks: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bookmarks initially, got %d", n)
	}

	if _, _, err := insertBookmark(db, "https://a.example/", "A", "", nil, 1000); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, _, err := insertBookmark(db, "https://b.example/", "B", "", nil, 2000); err != nil {
		t.Fatalf("insert: %v", err)
	}

	n, err = countBookmarks(db)
	if err != nil {
		t.Fatalf("countBookmarks: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bookmarks, got %d", n)
	}
}

func TestGetBookmark(t *testing.T) {
	db := testDB(t)
	id, _, err := insertBookmark(db, "https://a.example/", "A", "desc", []string{"one", "two"}, 1000)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	bm, ok, err := getBookmark(db, id)
	if err != nil {
		t.Fatalf("getBookmark: %v", err)
	}
	if !ok {
		t.Fatal("expected to find the inserted bookmark")
	}
	if bm.URL != "https://a.example/" || bm.Title != "A" || bm.Description != "desc" {
		t.Errorf("unexpected bookmark: %+v", bm)
	}
	if len(bm.Tags) != 2 {
		t.Errorf("expected 2 tags, got %v", bm.Tags)
	}

	_, ok, err = getBookmark(db, id+999)
	if err != nil {
		t.Fatalf("getBookmark missing: %v", err)
	}
	if ok {
		t.Error("expected ok=false for a missing id")
	}
}

func TestNormalizeTag(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"  Go  Lang  ", "go lang"},
		{"UPPER", "upper"},
		{"", ""},
		{"   ", ""},
		{"a\x00b", ""},
		{"tab\tcollapse", "tab collapse"},
	}
	for _, c := range cases {
		got := normalizeTag(c.in)
		if got != c.want {
			t.Errorf("normalizeTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	long := ""
	for i := 0; i < 40; i++ {
		long += "a"
	}
	got := normalizeTag(long)
	if len(got) != 32 {
		t.Errorf("normalizeTag should clamp to 32 chars, got length %d", len(got))
	}
}

func TestListBookmarksSearchMatchesTagNames(t *testing.T) {
	db := testDB(t)

	if _, _, err := insertBookmark(db, "https://example.com/a", "Untitled Entry", "no keyword here", []string{"devops", "kubernetes"}, 1000); err != nil {
		t.Fatalf("insert tagged: %v", err)
	}
	if _, _, err := insertBookmark(db, "https://example.com/b", "Another Entry", "also nothing", []string{"design"}, 1001); err != nil {
		t.Fatalf("insert other: %v", err)
	}

	// The tag is the ONLY place this term appears: not in url, title, or description.
	got, err := listBookmarks(db, "devops", nil)
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("search by tag name: got %d results, want 1", len(got))
	}
	if got[0].URL != "https://example.com/a" {
		t.Fatalf("search by tag name matched the wrong bookmark: %s", got[0].URL)
	}

	partial, err := listBookmarks(db, "kube", nil)
	if err != nil {
		t.Fatalf("listBookmarks partial: %v", err)
	}
	if len(partial) != 1 {
		t.Fatalf("partial tag match: got %d results, want 1", len(partial))
	}

	combined, err := listBookmarks(db, "devops", []string{"kubernetes"})
	if err != nil {
		t.Fatalf("listBookmarks combined: %v", err)
	}
	if len(combined) != 1 {
		t.Fatalf("tag search combined with tag filter: got %d, want 1", len(combined))
	}

	none, err := listBookmarks(db, "devops", []string{"design"})
	if err != nil {
		t.Fatalf("listBookmarks disjoint: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("tag search with a non-matching filter: got %d, want 0", len(none))
	}
}
