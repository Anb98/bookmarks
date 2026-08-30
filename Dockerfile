FROM golang:1.27-alpine AS build

RUN apk add --no-cache ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bookmarks .

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /bookmarks /bookmarks

ENV BOOKMARKS_ADDR=:8080 \
    BOOKMARKS_DB=/data/bookmarks.db

EXPOSE 8080

# A static Go binary needs no /etc/passwd entry, so a bare numeric id works on
# scratch. 1000:1000 matches the usual first host account; override it to match
# whoever owns the bind-mounted data directory.
USER 1000:1000

# Exec form needs no shell, which scratch does not have. The binary is its own
# health client via -healthcheck.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD ["/bookmarks", "-healthcheck"]

ENTRYPOINT ["/bookmarks"]
