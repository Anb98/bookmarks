package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlockedClassifiesAddresses(t *testing.T) {
	cases := []struct {
		addr    string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"169.254.169.254", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"100.64.0.1", true},
		{"198.18.0.1", true},
		{"192.0.0.1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"93.184.216.34", false},
	}
	for _, c := range cases {
		ip, err := netip.ParseAddr(c.addr)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", c.addr, err)
		}
		got := blocked(ip)
		if got != c.blocked {
			t.Errorf("blocked(%q) = %v, want %v", c.addr, got, c.blocked)
		}
	}
}

func TestDialerControlRefusesGarbageAddress(t *testing.T) {
	control := dialerControl(false)
	if err := control("tcp4", "not-an-address", nil); err == nil {
		t.Fatal("expected an error for an unparseable address, got nil")
	}
}

func TestDialerControlAllowsPrivateWhenConfigured(t *testing.T) {
	control := dialerControl(true)
	if err := control("tcp4", "127.0.0.1:80", nil); err != nil {
		t.Errorf("allowPrivate=true should permit loopback, got error: %v", err)
	}
}

func TestDialerControlBlocksPrivateByDefault(t *testing.T) {
	control := dialerControl(false)
	if err := control("tcp4", "127.0.0.1:80", nil); err == nil {
		t.Fatal("expected loopback to be blocked when allowPrivate=false")
	}
}

func TestFetchRejectsRedirectChainOverCap(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, r.URL.String(), http.StatusFound)
	}))
	defer server.Close()

	client := newFetchClient(true, 5*time.Second)
	_, err := client.Get(server.URL)
	if err == nil {
		t.Fatal("expected an error when the redirect chain exceeds the 5-hop cap")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("expected a redirect-cap error, got: %v", err)
	}
	if got := hits.Load(); got != 6 {
		t.Errorf("expected exactly 6 requests (1 original + 5 allowed redirects), got %d", got)
	}
}

func TestFetchBodyLimitedByReader(t *testing.T) {
	big := strings.Repeat("a", 10*1024*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(big))
	}))
	defer server.Close()

	client := newFetchClient(true, 5*time.Second)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, 1024)
	data, err := io.ReadAll(limited)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(data) != 1024 {
		t.Fatalf("expected limited read to stop at 1024 bytes, got %d", len(data))
	}
}

func TestFetchHonoursTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			w.Write([]byte("late"))
		}
	}))
	defer server.Close()

	client := newFetchClient(true, 50*time.Millisecond)
	start := time.Now()
	_, err := client.Get(server.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestFetchURLEndToEndBlocksLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html></html>"))
	}))
	defer server.Close()

	cfg := fetchConfig{
		allowPrivate: false,
		timeout:      2 * time.Second,
		maxBytes:     1024,
		userAgent:    "test-agent",
	}
	if _, err := fetchURL(context.Background(), cfg, server.URL); err == nil {
		t.Fatal("expected fetchURL to refuse a loopback httptest server by default")
	}
}

func TestFetchURLEndToEndAllowsPrivateWhenConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer server.Close()

	cfg := fetchConfig{
		allowPrivate: true,
		timeout:      2 * time.Second,
		maxBytes:     1024,
		userAgent:    "test-agent",
	}
	body, err := fetchURL(context.Background(), cfg, server.URL)
	if err != nil {
		t.Fatalf("fetchURL: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}
}

func TestFetchURLCapsOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("z", 2000)))
	}))
	defer server.Close()

	cfg := fetchConfig{
		allowPrivate: true,
		timeout:      2 * time.Second,
		maxBytes:     100,
		userAgent:    "test-agent",
	}
	body, err := fetchURL(context.Background(), cfg, server.URL)
	if err != nil {
		t.Fatalf("fetchURL: %v", err)
	}
	if len(body) > 100 {
		t.Fatalf("expected body capped at 100 bytes, got %d", len(body))
	}
}
