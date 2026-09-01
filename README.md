# Bookmarks

A self-hosted bookmarks manager in a single static binary. Paste a URL and the server fetches the page to fill in the title, description and favicon for you; find anything again by live search or by combining tags. Data lives in one SQLite file on your host, so backing it up means copying one file.

No authentication, by design. This is built for a machine you control on a network you trust — see [Security](#security) before exposing it.

## Quick path

```bash
mkdir -p data
docker compose up -d --build
```

Open <http://localhost:8080>.

To verify it worked:

```bash
curl -s localhost:8080/healthz     # -> ok
ls data/                           # -> bookmarks.db (plus -wal and -shm)
```

Change the published port with `BOOKMARKS_PORT=9000 docker compose up -d`.

### The one thing that trips people up

The container writes SQLite to a bind-mounted directory, so its user has to be able to write there. `compose.yaml` defaults to `1000:1000`. If your account is not uid 1000:

```bash
echo "UID=$(id -u)" >> .env
echo "GID=$(id -g)" >> .env
docker compose up -d
```

Get this wrong and the container exits at startup saying it cannot open the database. That is deliberate: the alternative is running as root and leaving root-owned files on your host that you then cannot read without `sudo`.

### Running the published image

Every push to `main` publishes a multi-arch image (`linux/amd64` and `linux/arm64`) to GitHub Container Registry, so you can skip the build entirely. The `ENTRYPOINT`, port and `/data` volume are the same ones compose wires up, and `:latest` follows `main` — pin the commit tag `ghcr.io/anb98/bookmarks:<sha>` if you would rather choose when to move.

```bash
mkdir -p data
docker pull ghcr.io/anb98/bookmarks:latest
docker run -d --name bookmarks --restart unless-stopped \
  -p 8080:8080 \
  -u "$(id -u):$(id -g)" \
  -v "$PWD/data:/data" \
  -e BOOKMARKS_ADDR=":8080" \
  -e BOOKMARKS_DB="/data/bookmarks.db" \
  ghcr.io/anb98/bookmarks:latest
```

GHCR creates the package as private on its first publish, so it has to be made public once in the repository's package settings before an unauthenticated `docker pull` will work.

## Configuration

Every setting is an environment variable, and the app starts with none of them set.

| Variable | Default | Meaning |
|---|---|---|
| `BOOKMARKS_ADDR` | `:8080` | Listen address inside the container |
| `BOOKMARKS_DB` | `/data/bookmarks.db` | SQLite file. WAL sidecars land beside it |
| `BOOKMARKS_THEME` | `dark` | Initial theme (`light` or `dark`) for a browser with no saved preference. Rejected at startup if it is anything else |
| `BOOKMARKS_ALLOW_PRIVATE_NET` | `false` | Allow metadata fetches to private/internal addresses. See [Security](#security) |
| `BOOKMARKS_FETCH_TIMEOUT` | `8s` | Timeout for one outbound metadata fetch |
| `BOOKMARKS_MAX_FETCH_BYTES` | `1048576` | Cap on a fetched page (1 MiB) |
| `BOOKMARKS_MAX_FAVICON_BYTES` | `65536` | Cap on a stored favicon (64 KiB) |
| `BOOKMARKS_MAX_IMPORT_BYTES` | `33554432` | Cap on an import upload (32 MiB) |
| `BOOKMARKS_USER_AGENT` | `Mozilla/5.0 (compatible; bookmarks/1.0)` | Some sites reject the default Go user agent |

The redirect cap (5 hops) is a constant, not a variable. It never needs tuning.

## Using it

| Action | How |
|---|---|
| Add a bookmark | `NEW`, paste a URL. Title and description fill in on their own and stay editable — whatever you type wins |
| Tag it | Type in the tag field. `MOST USED` shows existing tags; no match offers `+ create` |
| Search | Type in the search row. Matches title, URL, description and tags at once |
| Filter | Click tags in the band. Multiple tags combine with **AND** — a bookmark must carry all of them |
| Show the tags | Below 1024px the band starts collapsed to save vertical space. **Tap anywhere on the `TAGS` strip** to expand it — the whole strip is the control, not just `SHOW ALL` |
| Open a bookmark | Click the card. It is a real link, so middle-click and "open in new tab" work as expected |
| Edit or delete | Click the **pencil** at the card's corner — present at every width. `DELETE ENTRY` is permanent, with no trash |
| Export | `EXPORT` downloads the whole collection as JSON |
| Import | `IMPORT` accepts that JSON **or** a browser's `bookmarks.html` |

### Import behaviour

Import matches on URL and updates rather than duplicating. Title and description are only filled if currently empty; tags are merged. One bad row is skipped and reported, not fatal. A file that is not valid JSON or HTML is rejected without writing anything.

Browser bookmark folders become tags, since folders are the taxonomy you already built. Nesting contributes each level (`Work > Clients > Acme` yields three tags), and generic container names (`Bookmarks bar`, `Favorites`, `Other bookmarks`, and similar) are skipped.

**Import never fetches metadata.** A 500-row import would otherwise fire 500 outbound requests. Imported rows keep whatever the file gave them and show a monogram tile until you edit them.

## Security

### No authentication

There is no login, and anyone who can reach the port has full read and write access. Bind it to a trusted network, or put it behind a reverse proxy that handles authentication.

### SSRF is the real risk here

The metadata endpoint makes the **server** fetch a URL **you** supply. Combined with no authentication, anyone who can reach this app could otherwise use it to probe services that are only reachable from the server — cloud metadata endpoints, admin panels on localhost, devices on your LAN.

By default, fetches to these are refused:

- loopback (`127.0.0.0/8`, `::1`)
- private ranges (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`)
- link-local, including the cloud metadata address `169.254.169.254`
- `100.64.0.0/10` — carrier-grade NAT, **and the range Tailscale uses**
- `198.18.0.0/15` and `192.0.0.0/24`
- multicast and unspecified addresses
- any scheme other than `http` and `https`

Two details matter more than the list. The check runs against the **resolved IP**, not the hostname, so a domain pointed at `127.0.0.1` is still refused. And it re-runs on **every redirect hop**, so a public URL that redirects to an internal one dies on the second hop.

Set `BOOKMARKS_ALLOW_PRIVATE_NET=true` only if you deliberately want to bookmark internal pages and understand that it removes this protection. Saving a bookmark never required the fetch to succeed, so you can always add an internal URL by hand and type its title yourself.

### Other measures

| Concern | Measure |
|---|---|
| Stored titles rendered in the page | The frontend never uses `innerHTML`; all text goes through `textContent` |
| Favicons as an attack vector | Served only with an allowlisted image type, plus `nosniff` and `default-src 'none'; sandbox` |
| Third-party favicon services | None used. Only the site's own declared icon or `/favicon.ico`. If neither yields a real image, nothing is stored and a monogram renders |
| Oversized or slow responses | Timeout, byte cap, and a 5-hop redirect limit on every fetch |
| Import uploads | 32 MiB cap, per-row error collection, no outbound requests |

## Accessibility

Keyboard navigation and focus rings work throughout, and tag pills expose their frequency to screen readers (`"go, 6 bookmarks"`) because it is conveyed visually by size and brightness alone.

**One documented exception.** In the tag cloud, low-frequency tags are deliberately rendered dim and small — that contrast difference *is* the information channel, encoding how often each tag is used. Raising those tones to meet WCAG AA would erase the signal. This is a considered trade-off confined to the least important content on the page, not an oversight. Every other text tone, and the accent used for buttons and links, meets AA (4.5:1 or better).

## Development

```bash
go test ./...     # 74 tests, no external dependencies
go vet ./...
gofmt -l .

BOOKMARKS_DB=/tmp/dev.db BOOKMARKS_ADDR=:8080 go run .
```

Two dependencies: `modernc.org/sqlite` (pure Go, so the binary is genuinely static) and `golang.org/x/net/html` (for parsing untrusted remote HTML). The frontend is three hand-written files with no build step, no npm, and no framework, embedded into the binary with `go:embed` along with the fonts — the app makes no external network requests to render.

### Why a scratch image

| Stack | Base image floor, measured |
|---|---|
| **Go static + `scratch`** | **12.09 MB — the entire shipped image** |
| python:3.13-alpine | 70.3 MB before any application code |
| node:24-alpine | 234 MB before any application code |

Measure the built image with `docker history`, or `docker image inspect --format '{{.Size}}'` for the compressed size (~5.45 MB). Do not trust `docker images`, which reports inflated numbers for this image.

### Design artifacts

The frozen visual specification (the `*.dc.html` artboards and their supporting files) was removed once `web/` reproduced it. `go:embed` takes `web/` only, so the shipped UI is `web/index.html`, `web/app.css`, `web/app.js` and `web/fonts/`.
