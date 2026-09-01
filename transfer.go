package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const maxImportErrors = 20

// ImportResult reports the outcome of one import call.
type ImportResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

type importRow struct {
	URL         string
	Title       string
	Description string
	Tags        []string
	CreatedAt   int64
}

type exportBookmark struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	CreatedAt   int64    `json:"created_at"`
}

type exportDoc struct {
	Version    int              `json:"version"`
	ExportedAt string           `json:"exported_at"`
	Bookmarks  []exportBookmark `json:"bookmarks"`
}

// exportJSON deliberately excludes favicons: they are re-derivable from the
// stored domain, and base64-inlining them would bloat a text-diffable file
// roughly 100x. The .db file is the byte-exact backup artifact.
func exportJSON(db *sql.DB, now time.Time) ([]byte, error) {
	rows, err := listBookmarks(db, "", nil, "")
	if err != nil {
		return nil, err
	}
	doc := exportDoc{
		Version:    1,
		ExportedAt: now.UTC().Format(time.RFC3339),
		Bookmarks:  make([]exportBookmark, 0, len(rows)),
	}
	for _, bm := range rows {
		doc.Bookmarks = append(doc.Bookmarks, exportBookmark{
			URL:         bm.URL,
			Title:       bm.Title,
			Description: bm.Description,
			Tags:        bm.Tags,
			CreatedAt:   bm.CreatedAt,
		})
	}
	return json.Marshal(doc)
}

type importJSONDoc struct {
	Bookmarks []struct {
		URL         string   `json:"url"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		CreatedAt   int64    `json:"created_at"`
	} `json:"bookmarks"`
}

func importJSON(db *sql.DB, data []byte, now int64) (ImportResult, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return ImportResult{Errors: []string{}}, nil
	}

	var doc importJSONDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return ImportResult{}, fmt.Errorf("malformed json import: %w", err)
	}

	rows := make([]importRow, 0, len(doc.Bookmarks))
	for _, b := range doc.Bookmarks {
		rows = append(rows, importRow{
			URL:         b.URL,
			Title:       b.Title,
			Description: b.Description,
			Tags:        b.Tags,
			CreatedAt:   b.CreatedAt,
		})
	}
	return importRows(db, rows, now)
}

// genericFolderNames carry no taxonomic information: every browser creates
// them by default, so promoting them to tags would add noise to every import
// rather than reflecting anything the user actually organized.
var genericFolderNames = map[string]bool{
	"bookmarks":          true,
	"bookmarks bar":      true,
	"bookmarks menu":     true,
	"other bookmarks":    true,
	"favorites":          true,
	"favorites bar":      true,
	"mobile bookmarks":   true,
	"unsorted bookmarks": true,
	"bookmarks toolbar":  true,
}

func folderTags(stack []string) []string {
	var out []string
	for _, name := range stack {
		norm := normalizeTag(name)
		if norm == "" || genericFolderNames[norm] {
			continue
		}
		out = append(out, norm)
	}
	return out
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, v := range list {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// importNetscapeHTML parses the Netscape bookmarks format. Enclosing <H3>
// folder names become tags at every nesting level: browser folders are the
// user's existing taxonomy, and this app organizes by tags, so discarding
// them would destroy the only structure the user already built.
func importNetscapeHTML(db *sql.DB, data []byte, now int64) (ImportResult, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return ImportResult{Errors: []string{}}, nil
	}
	// A genuine Netscape export is always plain UTF-8 text; invalid UTF-8
	// is the practical signal that an unrelated/binary file was uploaded.
	if !utf8.Valid(data) {
		return ImportResult{}, fmt.Errorf("malformed netscape bookmarks file: invalid UTF-8")
	}

	rows, err := parseNetscapeBookmarks(data)
	if err != nil {
		return ImportResult{}, err
	}
	return importRows(db, rows, now)
}

func parseNetscapeBookmarks(data []byte) ([]importRow, error) {
	z := html.NewTokenizer(strings.NewReader(string(data)))

	var rows []importRow
	var stack []string
	pendingFolderName := ""
	curTag := ""
	var textBuf strings.Builder
	var curHref, curTagsAttr string

	finalizeDD := func() {
		if curTag == "dd" {
			desc := strings.TrimSpace(textBuf.String())
			if desc != "" && len(rows) > 0 {
				rows[len(rows)-1].Description = desc
			}
		}
		curTag = ""
		textBuf.Reset()
	}

loop:
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break loop
		}
		switch tt {
		case html.TextToken:
			if curTag == "a" || curTag == "h3" || curTag == "dd" {
				textBuf.WriteString(z.Token().Data)
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			switch tok.Data {
			case "a":
				finalizeDD()
				curTag = "a"
				textBuf.Reset()
				curHref = attrVal(tok, "href")
				curTagsAttr = attrVal(tok, "tags")
			case "h3":
				finalizeDD()
				curTag = "h3"
				textBuf.Reset()
			case "dd":
				finalizeDD()
				curTag = "dd"
				textBuf.Reset()
			case "dl":
				finalizeDD()
				stack = append(stack, pendingFolderName)
				pendingFolderName = ""
			}
		case html.EndTagToken:
			tok := z.Token()
			switch tok.Data {
			case "a":
				title := strings.TrimSpace(textBuf.String())
				curTag = ""
				textBuf.Reset()
				var explicit []string
				if curTagsAttr != "" {
					explicit = strings.Split(curTagsAttr, ",")
				}
				rows = append(rows, importRow{
					URL:   curHref,
					Title: title,
					Tags:  unionStrings(folderTags(stack), explicit),
				})
			case "h3":
				pendingFolderName = strings.TrimSpace(textBuf.String())
				curTag = ""
				textBuf.Reset()
			case "dl":
				finalizeDD()
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
	}
	finalizeDD()

	return rows, nil
}

// importRows applies the shared upsert semantics for both import formats: a
// single invalid row is collected as an error (capped) rather than aborting
// the whole import, since the file itself already parsed successfully.
func importRows(db *sql.DB, rows []importRow, now int64) (ImportResult, error) {
	res := ImportResult{Errors: []string{}}
	err := withTx(db, func(tx *sql.Tx) error {
		for i, row := range rows {
			normURL, err := normalizeURL(row.URL)
			if err != nil {
				if len(res.Errors) < maxImportErrors {
					res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", i+1, err))
				}
				continue
			}
			createdAt := row.CreatedAt
			if createdAt <= 0 {
				createdAt = now
			}
			outcome, err := upsertImportRow(tx, normURL, row.Title, row.Description, row.Tags, createdAt, now)
			if err != nil {
				return err
			}
			switch outcome {
			case "created":
				res.Created++
			case "updated":
				res.Updated++
			default:
				res.Skipped++
			}
		}
		return nil
	})
	return res, err
}

// upsertImportRow fills title/description only if currently empty and unions
// tags, so re-importing an unmodified export is a stable no-op ("skipped").
func upsertImportRow(tx *sql.Tx, url, title, description string, tags []string, createdAt, now int64) (string, error) {
	var id int64
	var curTitle, curDesc string
	lookupErr := tx.QueryRow(`SELECT id, title, description FROM bookmarks WHERE url = ?`, url).Scan(&id, &curTitle, &curDesc)

	if lookupErr == sql.ErrNoRows {
		domain := domainOf(url)
		searchText := computeSearchText(url, title, description)
		res, err := tx.Exec(
			`INSERT INTO bookmarks(url, title, description, domain, search_text, created_at, updated_at) VALUES(?,?,?,?,?,?,?)`,
			url, title, description, domain, searchText, createdAt, now,
		)
		if err != nil {
			return "", err
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return "", err
		}
		if err := attachTags(tx, newID, tags); err != nil {
			return "", err
		}
		return "created", nil
	}
	if lookupErr != nil {
		return "", lookupErr
	}

	newTitle, newDesc := curTitle, curDesc
	changed := false
	if curTitle == "" && title != "" {
		newTitle = title
		changed = true
	}
	if curDesc == "" && description != "" {
		newDesc = description
		changed = true
	}

	existingTags, err := existingTagSet(tx, id)
	if err != nil {
		return "", err
	}
	var newTags []string
	for _, t := range normalizeTags(tags) {
		if !existingTags[t] {
			newTags = append(newTags, t)
			changed = true
		}
	}

	if !changed {
		return "skipped", nil
	}

	if newTitle != curTitle || newDesc != curDesc {
		domain := domainOf(url)
		searchText := computeSearchText(url, newTitle, newDesc)
		if _, err := tx.Exec(
			`UPDATE bookmarks SET title=?, description=?, domain=?, search_text=?, updated_at=? WHERE id=?`,
			newTitle, newDesc, domain, searchText, now, id,
		); err != nil {
			return "", err
		}
	}
	if len(newTags) > 0 {
		if err := attachTags(tx, id, newTags); err != nil {
			return "", err
		}
	}
	return "updated", nil
}

func existingTagSet(tx *sql.Tx, bookmarkID int64) (map[string]bool, error) {
	rows, err := tx.Query(
		`SELECT t.name FROM tags t JOIN bookmark_tags bt ON bt.tag_id = t.id WHERE bt.bookmark_id = ?`, bookmarkID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		set[name] = true
	}
	return set, rows.Err()
}
