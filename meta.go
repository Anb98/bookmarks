package main

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Metadata is the page metadata discovered by extractMetadata.
type Metadata struct {
	Title       string
	Description string
	IconHref    string
}

var (
	// An explicit "scheme://" prefix.
	schemePrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*://`)
	// "host:1234" — a colon followed by digits is a port, not a scheme.
	hostPortPrefix = regexp.MustCompile(`^[^/?#:]+:[0-9]+([/?#]|$)`)
	// A scheme with no "//" and no dot in its name: mailto:, javascript:, data:.
	opaqueSchemePrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+\-]*:`)
	// Something that actually looks like a host: dotted, or bare localhost.
	hostLike = regexp.MustCompile(`^(localhost|[^/?#:]+\.[^/?#:]+)(:[0-9]+)?([/?#]|$)`)
)

// defaultScheme prepends https:// when the input is a bare host rather than a
// full URL. People paste "go.dev" far more often than "https://go.dev/", and
// rejecting that made metadata inference silently do nothing for the most
// common input. It deliberately does NOT coerce anything else: an opaque
// scheme like mailto: is left alone so the http(s) check below rejects it, and
// a bare word like "not-a-valid-url" is not a host and stays invalid.
func defaultScheme(raw string) string {
	switch {
	case schemePrefix.MatchString(raw):
		return raw
	case hostPortPrefix.MatchString(raw):
		return "https://" + raw
	case opaqueSchemePrefix.MatchString(raw):
		return raw
	case hostLike.MatchString(raw):
		return "https://" + raw
	default:
		return raw
	}
}

// normalizeURL is what makes UNIQUE(url) meaningful: it rejects non-http(s)
// schemes and canonicalizes scheme, host, default port, path, and fragment
// while preserving the query.
func normalizeURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("missing url")
	}
	rawURL = defaultScheme(rawURL)

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Scheme = scheme

	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}

	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "" {
		u.Path = "/"
	}

	return u.String(), nil
}

func domainOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func monogramInitial(domain string) string {
	for _, r := range domain {
		return strings.ToUpper(string(r))
	}
	return ""
}

func isIconRel(rel string) bool {
	rel = strings.ToLower(strings.TrimSpace(rel))
	if rel == "icon" || rel == "shortcut icon" || rel == "apple-touch-icon" {
		return true
	}
	for _, tok := range strings.Fields(rel) {
		if tok == "icon" || tok == "apple-touch-icon" {
			return true
		}
	}
	return false
}

func attrVal(tok html.Token, key string) string {
	for _, a := range tok.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// extractMetadata scans HTML for title/description candidates, stopping at
// </head> or the first <body>. It uses the real HTML5 tokenizer rather than
// a hand-rolled scanner so that comments, script/RCDATA content, unquoted
// and single-quoted attributes, and out-of-order attributes are all handled
// per spec instead of by ad hoc string matching.
func extractMetadata(r io.Reader) (Metadata, error) {
	var ogTitle, ogDesc, plainTitle, plainDesc, twitterTitle, twitterDesc, icon string
	inTitle := false

	z := html.NewTokenizer(r)
loop:
	for {
		switch z.Next() {
		case html.ErrorToken:
			break loop
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			switch tok.Data {
			case "body":
				break loop
			case "title":
				inTitle = true
			case "meta":
				content := attrVal(tok, "content")
				switch strings.ToLower(attrVal(tok, "name")) {
				case "description":
					if plainDesc == "" {
						plainDesc = content
					}
				case "twitter:title":
					if twitterTitle == "" {
						twitterTitle = content
					}
				case "twitter:description":
					if twitterDesc == "" {
						twitterDesc = content
					}
				}
				switch strings.ToLower(attrVal(tok, "property")) {
				case "og:title":
					if ogTitle == "" {
						ogTitle = content
					}
				case "og:description":
					if ogDesc == "" {
						ogDesc = content
					}
				}
			case "link":
				if icon == "" && isIconRel(attrVal(tok, "rel")) {
					icon = attrVal(tok, "href")
				}
			}
		case html.EndTagToken:
			tok := z.Token()
			switch tok.Data {
			case "title":
				inTitle = false
			case "head":
				break loop
			}
		case html.TextToken:
			if inTitle && plainTitle == "" {
				plainTitle = z.Token().Data
			}
		}
	}
	if err := z.Err(); err != nil && err != io.EOF {
		return Metadata{}, err
	}

	return Metadata{
		Title:       cleanText(firstNonEmpty(ogTitle, plainTitle, twitterTitle)),
		Description: cleanText(firstNonEmpty(ogDesc, plainDesc, twitterDesc)),
		IconHref:    strings.TrimSpace(icon),
	}, nil
}

// cp1252HighTable maps CP1252 bytes 0x80-0x9F to their Unicode code points.
// Bytes 0x00-0x7F and 0xA0-0xFF are Latin-1 identity (byte value == code point).
var cp1252HighTable = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

func repairCP1252(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 && c <= 0x9F {
			b.WriteRune(cp1252HighTable[c-0x80])
		} else {
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}

func cleanText(s string) string {
	if s == "" {
		return s
	}
	if !utf8.ValidString(s) {
		s = repairCP1252(s)
	}
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > 512 {
		runes = runes[:512]
	}
	return string(runes)
}
