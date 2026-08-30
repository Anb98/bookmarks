package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

const maxRedirects = 5

// extraBlockedRanges covers ranges the stdlib netip IsPrivate/IsLinkLocal
// family of helpers do not classify as reserved, but which still route to
// infrastructure that must never be reachable from an unauthenticated fetch.
var extraBlockedRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // RFC6598 CGNAT; this is Tailscale's range
	netip.MustParsePrefix("198.18.0.0/15"), // RFC2544 benchmarking
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
}

// blocked reports whether ip must never be dialed by the fetch client.
func blocked(ip netip.Addr) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	unmapped := ip.Unmap()
	for _, p := range extraBlockedRanges {
		if p.Contains(unmapped) {
			return true
		}
	}
	return false
}

// dialerControl builds a net.Dialer.Control that fires on every dial,
// including every redirect hop, since it receives the concrete post-DNS
// address rather than the request hostname string. This defeats both DNS
// rebinding and redirect-based SSRF bypasses.
func dialerControl(allowPrivate bool) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		if allowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("unparseable address %q", host)
		}
		if blocked(ip) {
			return fmt.Errorf("blocked address %s", ip)
		}
		return nil
	}
}

func newFetchClient(allowPrivate bool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: dialerControl(allowPrivate),
	}
	transport := &http.Transport{
		DialContext: dialer.DialContext,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		},
	}
}

type fetchConfig struct {
	allowPrivate bool
	timeout      time.Duration
	maxBytes     int64
	userAgent    string
}

func fetchURL(ctx context.Context, cfg fetchConfig, rawURL string) ([]byte, error) {
	client := newFetchClient(cfg.allowPrivate, cfg.timeout)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", cfg.userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cfg.maxBytes))
	if err != nil {
		return nil, err
	}
	return body, nil
}
