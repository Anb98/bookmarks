package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

const schemaVersion = 2

const schemaV1 = `
CREATE TABLE bookmarks (
  id INTEGER PRIMARY KEY,
  url TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  domain TEXT NOT NULL DEFAULT '',
  search_text TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX bookmarks_created_idx ON bookmarks(created_at DESC, id DESC);
CREATE INDEX bookmarks_domain_idx ON bookmarks(domain);
CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE bookmark_tags (
  bookmark_id INTEGER NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (bookmark_id, tag_id)
) WITHOUT ROWID;
CREATE INDEX bookmark_tags_tag_idx ON bookmark_tags(tag_id, bookmark_id);
CREATE TABLE favicons (
  domain TEXT PRIMARY KEY, mime TEXT NOT NULL, etag TEXT NOT NULL,
  bytes BLOB NOT NULL, fetched_at INTEGER NOT NULL
) WITHOUT ROWID;
`

// schemaV2 adds the visit counter behind the "most used" sort. It is bumped by
// a beacon the card fires on open, so it is advisory: a count lost to a closed
// tab or a blocked beacon only reorders one row.
const schemaV2 = `
ALTER TABLE bookmarks ADD COLUMN visit_count INTEGER NOT NULL DEFAULT 0;
CREATE INDEX bookmarks_visits_idx ON bookmarks(visit_count DESC, created_at DESC);
`

// Bookmark is the shape returned by list/read operations and JSON responses.
type Bookmark struct {
	ID          int64    `json:"id"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Domain      string   `json:"domain"`
	Tags        []string `json:"tags"`
	HasIcon     bool     `json:"has_icon"`
	CreatedAt   int64    `json:"created_at"`
}

// TagCount is one entry of the always-global, unfiltered tag cloud.
type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Favicon is a cached icon for a domain, keyed by domain.
type Favicon struct {
	Domain    string
	MIME      string
	ETag      string
	Bytes     []byte
	FetchedAt int64
}

func openDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than the %d this binary supports; refusing to start", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}

	return withTx(db, func(tx *sql.Tx) error {
		if version < 1 {
			if _, err := tx.Exec(schemaV1); err != nil {
				return err
			}
		}
		if version < 2 {
			if _, err := tx.Exec(schemaV2); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
			return err
		}
		return nil
	})
}

func withTx(db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func computeSearchText(url, title, description string) string {
	return strings.ToLower(url + " " + title + " " + description)
}

func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	for _, r := range s {
		if unicode.IsControl(r) {
			return ""
		}
	}
	runes := []rune(s)
	if len(runes) > 32 {
		runes = runes[:32]
	}
	return string(runes)
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		name := normalizeTag(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func attachTags(tx *sql.Tx, bookmarkID int64, tags []string) error {
	for _, name := range normalizeTags(tags) {
		if _, err := tx.Exec(`INSERT INTO tags(name) VALUES(?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
			return err
		}
		var tagID int64
		if err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO bookmark_tags(bookmark_id, tag_id) VALUES(?, ?) ON CONFLICT DO NOTHING`, bookmarkID, tagID); err != nil {
			return err
		}
	}
	return nil
}

func sweepOrphanTags(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM bookmark_tags)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM favicons WHERE domain NOT IN (SELECT domain FROM bookmarks)`); err != nil {
		return err
	}
	return nil
}

// insertBookmark upserts by normalized URL: an existing URL returns its
// existing id with existed=true and creates no duplicate row.
func insertBookmark(db *sql.DB, url, title, description string, tags []string, now int64) (id int64, existed bool, err error) {
	err = withTx(db, func(tx *sql.Tx) error {
		var existingID int64
		lookupErr := tx.QueryRow(`SELECT id FROM bookmarks WHERE url = ?`, url).Scan(&existingID)
		if lookupErr == nil {
			id, existed = existingID, true
			return nil
		}
		if lookupErr != sql.ErrNoRows {
			return lookupErr
		}

		domain := domainOf(url)
		searchText := computeSearchText(url, title, description)
		res, err := tx.Exec(
			`INSERT INTO bookmarks(url, title, description, domain, search_text, created_at, updated_at) VALUES(?,?,?,?,?,?,?)`,
			url, title, description, domain, searchText, now, now,
		)
		if err != nil {
			return err
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := attachTags(tx, newID, tags); err != nil {
			return err
		}
		id = newID
		return nil
	})
	return id, existed, err
}

func updateBookmark(db *sql.DB, id int64, url, title, description string, tags []string, now int64) (found bool, conflict bool, err error) {
	err = withTx(db, func(tx *sql.Tx) error {
		var exists int
		lookupErr := tx.QueryRow(`SELECT 1 FROM bookmarks WHERE id = ?`, id).Scan(&exists)
		if lookupErr == sql.ErrNoRows {
			return nil
		}
		if lookupErr != nil {
			return lookupErr
		}
		found = true

		var conflictID int64
		conflictErr := tx.QueryRow(`SELECT id FROM bookmarks WHERE url = ? AND id != ?`, url, id).Scan(&conflictID)
		if conflictErr == nil {
			conflict = true
			return nil
		}
		if conflictErr != sql.ErrNoRows {
			return conflictErr
		}

		domain := domainOf(url)
		searchText := computeSearchText(url, title, description)
		if _, err := tx.Exec(
			`UPDATE bookmarks SET url=?, title=?, description=?, domain=?, search_text=?, updated_at=? WHERE id=?`,
			url, title, description, domain, searchText, now, id,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM bookmark_tags WHERE bookmark_id = ?`, id); err != nil {
			return err
		}
		if err := attachTags(tx, id, tags); err != nil {
			return err
		}
		return sweepOrphanTags(tx)
	})
	return found, conflict, err
}

func deleteBookmark(db *sql.DB, id int64) (found bool, err error) {
	err = withTx(db, func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM bookmarks WHERE id = ?`, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		found = true
		return sweepOrphanTags(tx)
	})
	return found, err
}

// recordVisit bumps the advisory counter behind the "most used" sort. A
// missing id is not an error: the beacon races deletion and there is nothing
// the caller could do about it anyway.
func recordVisit(db *sql.DB, id int64) error {
	_, err := db.Exec(`UPDATE bookmarks SET visit_count = visit_count + 1 WHERE id = ?`, id)
	return err
}

func countBookmarks(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM bookmarks`).Scan(&n)
	return n, err
}

func getBookmark(db *sql.DB, id int64) (Bookmark, bool, error) {
	var bm Bookmark
	var tagList sql.NullString
	var hasIcon int64
	err := db.QueryRow(fmt.Sprintf(`
		SELECT b.id, b.url, b.title, b.description, b.domain, b.created_at, %s
		FROM bookmarks b WHERE b.id = ?`, tagListColumns), id,
	).Scan(&bm.ID, &bm.URL, &bm.Title, &bm.Description, &bm.Domain, &bm.CreatedAt, &tagList, &hasIcon)
	if err == sql.ErrNoRows {
		return Bookmark{}, false, nil
	}
	if err != nil {
		return Bookmark{}, false, err
	}
	bm.HasIcon = hasIcon != 0
	if tagList.Valid && tagList.String != "" {
		bm.Tags = strings.Split(tagList.String, "\x1f")
	} else {
		bm.Tags = []string{}
	}
	return bm, true, nil
}

const tagListColumns = `
	(SELECT group_concat(t2.name, char(31) ORDER BY t2.name)
		FROM bookmark_tags x JOIN tags t2 ON t2.id = x.tag_id
		WHERE x.bookmark_id = b.id) AS tag_list,
	EXISTS(SELECT 1 FROM favicons f WHERE f.domain = b.domain) AS has_icon`

// searchPredicate matches the query against url, title and description via the
// precomputed search_text column, and separately against tag names. Tags are
// deliberately not folded into search_text: they live in their own table and
// change independently of the bookmark row, so denormalizing them would mean
// recomputing search_text on every tag edit.
const searchPredicate = `(b.search_text LIKE ? OR EXISTS (
		SELECT 1 FROM bookmark_tags sbt JOIN tags st ON st.id = sbt.tag_id
		WHERE sbt.bookmark_id = b.id AND st.name LIKE ?))`

// sortOrders is a closed whitelist: the value reaches SQL by string
// substitution, so an unknown key must never fall through to the query.
// "title" orders by what the card actually prints, which is the url whenever
// the title is empty.
var sortOrders = map[string]string{
	"recent": "b.created_at DESC, b.id DESC",
	"oldest": "b.created_at ASC, b.id ASC",
	"title":  "(CASE WHEN b.title = '' THEN b.url ELSE b.title END) COLLATE NOCASE ASC, b.id DESC",
	"used":   "b.visit_count DESC, b.created_at DESC, b.id DESC",
}

const defaultSort = "recent"

// orderClause resolves a sort key, falling back to the default for anything
// unrecognized rather than erroring: a stale key in someone's localStorage
// should render a list, not a 400.
func orderClause(sort string) string {
	if clause, ok := sortOrders[sort]; ok {
		return clause
	}
	return sortOrders[defaultSort]
}

// listBookmarks applies AND tag-filter semantics plus the search predicate.
// Zero selected tags omits both joins and the HAVING clause entirely: keeping
// them would silently drop every untagged bookmark.
func listBookmarks(db *sql.DB, q string, tags []string, sort string) ([]Bookmark, error) {
	pattern := "%" + strings.ToLower(q) + "%"
	order := orderClause(sort)

	var rows *sql.Rows
	var err error
	if len(tags) == 0 {
		rows, err = db.Query(fmt.Sprintf(`
			SELECT b.id, b.url, b.title, b.description, b.domain, b.created_at, %s
			FROM bookmarks b
			WHERE %s
			ORDER BY %s`, tagListColumns, searchPredicate, order), pattern, pattern)
	} else {
		placeholders := make([]string, len(tags))
		args := make([]any, 0, len(tags)+3)
		for i, t := range tags {
			placeholders[i] = "?"
			args = append(args, t)
		}
		args = append(args, pattern, pattern, len(tags))
		query := fmt.Sprintf(`
			SELECT b.id, b.url, b.title, b.description, b.domain, b.created_at, %s
			FROM bookmarks b
			JOIN bookmark_tags bt ON bt.bookmark_id = b.id
			JOIN tags t ON t.id = bt.tag_id
			WHERE t.name IN (%s) AND %s
			GROUP BY b.id
			HAVING COUNT(DISTINCT t.id) = ?
			ORDER BY %s`, tagListColumns, strings.Join(placeholders, ","), searchPredicate, order)
		rows, err = db.Query(query, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []Bookmark{}
	for rows.Next() {
		var bm Bookmark
		var tagList sql.NullString
		var hasIcon int64
		if err := rows.Scan(&bm.ID, &bm.URL, &bm.Title, &bm.Description, &bm.Domain, &bm.CreatedAt, &tagList, &hasIcon); err != nil {
			return nil, err
		}
		bm.HasIcon = hasIcon != 0
		if tagList.Valid && tagList.String != "" {
			bm.Tags = strings.Split(tagList.String, "\x1f")
		} else {
			bm.Tags = []string{}
		}
		result = append(result, bm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// tagCounts is always global and unfiltered: the tag cloud shows all tags at
// all times regardless of the current search or tag selection.
func tagCounts(db *sql.DB) ([]TagCount, error) {
	rows, err := db.Query(`
		SELECT t.name, COUNT(bt.bookmark_id)
		FROM tags t JOIN bookmark_tags bt ON bt.tag_id = t.id
		GROUP BY t.id ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []TagCount{}
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Name, &tc.Count); err != nil {
			return nil, err
		}
		result = append(result, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func getFavicon(db *sql.DB, domain string) (Favicon, bool, error) {
	var fv Favicon
	err := db.QueryRow(
		`SELECT domain, mime, etag, bytes, fetched_at FROM favicons WHERE domain = ?`, domain,
	).Scan(&fv.Domain, &fv.MIME, &fv.ETag, &fv.Bytes, &fv.FetchedAt)
	if err == sql.ErrNoRows {
		return Favicon{}, false, nil
	}
	if err != nil {
		return Favicon{}, false, err
	}
	return fv, true, nil
}

func putFavicon(db *sql.DB, domain, mime string, data []byte, now int64) error {
	etag := fmt.Sprintf("%x", sha256.Sum256(data))
	_, err := db.Exec(`
		INSERT INTO favicons(domain, mime, etag, bytes, fetched_at) VALUES(?,?,?,?,?)
		ON CONFLICT(domain) DO UPDATE SET mime=excluded.mime, etag=excluded.etag, bytes=excluded.bytes, fetched_at=excluded.fetched_at`,
		domain, mime, etag, data, now)
	return err
}
