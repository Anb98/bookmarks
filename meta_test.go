package main

import (
	"strings"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"HTTP://Example.COM/Path", "http://example.com/Path", false},
		{"https://Example.com:443/a", "https://example.com/a", false},
		{"http://example.com:80/a", "http://example.com/a", false},
		{"https://example.com:8443/a", "https://example.com:8443/a", false},
		{"https://example.com/a#fragment", "https://example.com/a", false},
		{"https://example.com", "https://example.com/", false},
		{"https://example.com?q=1&x=2", "https://example.com/?q=1&x=2", false},
		{"ftp://example.com/file", "", true},
		{"not a url at all\x7f", "", true},
		{"mailto:a@b.com", "", true},
	}
	for _, c := range cases {
		got, err := normalizeURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeURL(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeURL(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDomainOf(t *testing.T) {
	cases := map[string]string{
		"https://Example.COM/a":         "example.com",
		"http://sub.example.com:8080/x": "sub.example.com",
		"not a url":                     "",
	}
	for in, want := range cases {
		got := domainOf(in)
		if got != want {
			t.Errorf("domainOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMonogramInitial(t *testing.T) {
	cases := map[string]string{
		"example.com": "E",
		"google.com":  "G",
		"":            "",
	}
	for in, want := range cases {
		got := monogramInitial(in)
		if got != want {
			t.Errorf("monogramInitial(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractMetadataPrecedence(t *testing.T) {
	html := `<html><head>
		<title>Plain Title</title>
		<meta name="description" content="Plain description">
		<meta property="og:title" content="OG Title">
		<meta property="og:description" content="OG description">
		<meta name="twitter:title" content="Twitter Title">
		<meta name="twitter:description" content="Twitter description">
	</head><body></body></html>`

	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.Title != "OG Title" {
		t.Errorf("Title = %q, want og:title to win", md.Title)
	}
	if md.Description != "OG description" {
		t.Errorf("Description = %q, want og:description to win", md.Description)
	}
}

func TestExtractMetadataFallsBackToPlainThenTwitter(t *testing.T) {
	html := `<html><head>
		<title>Plain Title</title>
		<meta name="description" content="Plain description">
		<meta name="twitter:title" content="Twitter Title">
		<meta name="twitter:description" content="Twitter description">
	</head><body></body></html>`
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.Title != "Plain Title" {
		t.Errorf("Title = %q, want plain title (no og present)", md.Title)
	}
	if md.Description != "Plain description" {
		t.Errorf("Description = %q, want plain description", md.Description)
	}

	html2 := `<html><head>
		<meta name="twitter:title" content="Twitter Only Title">
		<meta name="twitter:description" content="Twitter Only description">
	</head><body></body></html>`
	md2, err := extractMetadata(strings.NewReader(html2))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md2.Title != "Twitter Only Title" {
		t.Errorf("Title = %q, want twitter title as last resort", md2.Title)
	}
	if md2.Description != "Twitter Only description" {
		t.Errorf("Description = %q, want twitter description as last resort", md2.Description)
	}
}

func TestExtractMetadataIconLink(t *testing.T) {
	html := `<html><head>
		<link rel="shortcut icon" href="/favicon.ico">
	</head><body></body></html>`
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.IconHref != "/favicon.ico" {
		t.Errorf("IconHref = %q, want /favicon.ico", md.IconHref)
	}

	html2 := `<html><head><link rel="apple-touch-icon" href="/apple.png"></head><body></body></html>`
	md2, err := extractMetadata(strings.NewReader(html2))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md2.IconHref != "/apple.png" {
		t.Errorf("IconHref = %q, want /apple.png", md2.IconHref)
	}
}

func TestExtractMetadataIgnoresMetaInsideComment(t *testing.T) {
	html := `<html><head>
		<!-- <meta name="description" content="Should not appear"> -->
		<title>Real Title</title>
	</head><body></body></html>`
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.Description != "" {
		t.Errorf("Description = %q, want empty (meta was inside a comment)", md.Description)
	}
	if md.Title != "Real Title" {
		t.Errorf("Title = %q, want %q", md.Title, "Real Title")
	}
}

func TestExtractMetadataIgnoresMetaInsideScript(t *testing.T) {
	html := `<html><head>
		<script>var x = '<meta name="description" content="Should not appear">';</script>
		<title>Real Title</title>
	</head><body></body></html>`
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.Description != "" {
		t.Errorf("Description = %q, want empty (meta was inside a script)", md.Description)
	}
	if md.Title != "Real Title" {
		t.Errorf("Title = %q, want %q", md.Title, "Real Title")
	}
}

func TestExtractMetadataUnquotedAttributeValues(t *testing.T) {
	html := `<html><head><meta name=description content=Unquoted-description></head><body></body></html>`
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.Description != "Unquoted-description" {
		t.Errorf("Description = %q, want %q", md.Description, "Unquoted-description")
	}
}

func TestExtractMetadataSingleQuotedAttributeValues(t *testing.T) {
	html := `<html><head><meta name='description' content='Single quoted'></head><body></body></html>`
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.Description != "Single quoted" {
		t.Errorf("Description = %q, want %q", md.Description, "Single quoted")
	}
}

func TestExtractMetadataReversedAttributeOrder(t *testing.T) {
	html := `<html><head><meta content="Reversed order description" name="description"></head><body></body></html>`
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.Description != "Reversed order description" {
		t.Errorf("Description = %q, want %q", md.Description, "Reversed order description")
	}
}

func TestExtractMetadataMissingClosingHead(t *testing.T) {
	html := `<html><head><title>No Closing Head</title><meta name="description" content="desc"><body><p>content</p></body></html>`
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if md.Title != "No Closing Head" {
		t.Errorf("Title = %q, want %q", md.Title, "No Closing Head")
	}
	if md.Description != "desc" {
		t.Errorf("Description = %q, want %q", md.Description, "desc")
	}
}

func TestExtractMetadataRepairsCP1252(t *testing.T) {
	// 0x92 is CP1252's right single quotation mark; invalid as standalone UTF-8.
	raw := "<html><head><title>It\x92s a title</title></head><body></body></html>"
	md, err := extractMetadata(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	want := "It’s a title"
	if md.Title != want {
		t.Errorf("Title = %q, want %q", md.Title, want)
	}
}

func TestExtractMetadataTrimsCollapsesAndClamps(t *testing.T) {
	longTitle := "  Lots   of   whitespace   " + strings.Repeat("x", 600) + "  "
	html := "<html><head><title>" + longTitle + "</title></head><body></body></html>"
	md, err := extractMetadata(strings.NewReader(html))
	if err != nil {
		t.Fatalf("extractMetadata: %v", err)
	}
	if strings.Contains(md.Title, "  ") {
		t.Errorf("Title still contains double spaces: %q", md.Title)
	}
	if len([]rune(md.Title)) != 512 {
		t.Errorf("Title length = %d, want clamped to 512", len([]rune(md.Title)))
	}
}

func TestNormalizeURLDefaultsMissingSchemeToHTTPS(t *testing.T) {
	coerced := map[string]string{
		"go.dev":                   "https://go.dev/",
		"www.go.dev":               "https://www.go.dev/",
		"go.dev/blog":              "https://go.dev/blog",
		"GO.DEV":                   "https://go.dev/",
		"  go.dev  ":               "https://go.dev/",
		"sub.domain.example.co.uk": "https://sub.domain.example.co.uk/",
		"example.com/a?b=c":        "https://example.com/a?b=c",
	}
	for in, want := range coerced {
		got, err := normalizeURL(in)
		if err != nil {
			t.Errorf("normalizeURL(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeURL(%q) = %q, want %q", in, got, want)
		}
	}

	// host:port is a port, not a scheme.
	for in, want := range map[string]string{
		"example.com:8080/x": "https://example.com:8080/x",
		"localhost:3000":     "https://localhost:3000/",
	} {
		if got, err := normalizeURL(in); err != nil || got != want {
			t.Errorf("normalizeURL(%q) = %q err %v, want %q", in, got, err, want)
		}
	}

	// An explicit scheme must still be honoured, and a bad one still rejected.
	if got, err := normalizeURL("http://go.dev/"); err != nil || got != "http://go.dev/" {
		t.Errorf("explicit http scheme: got %q err %v", got, err)
	}
	for _, bad := range []string{
		"file:///etc/passwd",
		"ftp://example.com/",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"mailto:a@b.com",
		// a bare word is not a host and must not be coerced into one
		"not-a-valid-url",
		"just some text",
		"",
		"   ",
		"https://",
	} {
		if got, err := normalizeURL(bad); err == nil {
			t.Errorf("normalizeURL(%q) should have failed, got %q", bad, got)
		}
	}
}
