package server

import (
	"compress/gzip"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestExtractIP pins down the client IP trust order.
//
// The important case is the spoofed X-Forwarded-For: the first entry is
// controlled by the client, so it must never be used. Taking it would let
// an attacker walk straight through the brute-force rate limiter.
func TestExtractIP(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		remote  string
		want    string
	}{
		{
			name: "cloudflare header wins",
			headers: map[string]string{
				"Cf-Connecting-Ip": "203.0.113.5",
				"X-Forwarded-For":  "6.6.6.6, 203.0.113.9",
			},
			remote: "10.0.0.1:1234",
			want:   "203.0.113.5",
		},
		{
			name:    "forged first xff entry is ignored, last one is trusted",
			headers: map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7"},
			remote:  "10.0.0.1:1234",
			want:    "198.51.100.7",
		},
		{
			name:    "invalid cloudflare header falls through to xff",
			headers: map[string]string{"Cf-Connecting-Ip": "not-an-ip", "X-Forwarded-For": "198.51.100.7"},
			remote:  "10.0.0.1:1234",
			want:    "198.51.100.7",
		},
		{
			name:    "garbage xff falls back to the peer address",
			headers: map[string]string{"X-Forwarded-For": "unknown"},
			remote:  "192.0.2.10:5555",
			want:    "192.0.2.10",
		},
		{
			name:    "no headers at all falls back to the peer address",
			headers: nil,
			remote:  "192.0.2.10:5555",
			want:    "192.0.2.10",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/report", nil)
			r.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := extractIP(r); got != tc.want {
				t.Fatalf("extractIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWriteJSONCacheAndGzip checks the two CDN related guarantees of the
// API responses: they are never cacheable, and large payloads are
// compressed at the origin.
func TestWriteJSONCacheAndGzip(t *testing.T) {
	// Small payload: served as is.
	r := httptest.NewRequest("GET", "/api/servers", nil)
	w := httptest.NewRecorder()
	writeJSON(w, r, 200, map[string]string{"status": "ok"})

	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for a small payload", got)
	}

	// Large payload with a gzip capable client: compressed.
	big := make([]map[string]float64, 400)
	for i := range big {
		big[i] = map[string]float64{"cpu_percent": float64(i), "mem_percent": float64(i) * 1.5}
	}

	r2 := httptest.NewRequest("GET", "/api/history/web-01", nil)
	r2.Header.Set("Accept-Encoding", "gzip")
	w2 := httptest.NewRecorder()
	writeJSON(w2, r2, 200, big)

	if got := w2.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want %q", got, "gzip")
	}
	if got := w2.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}

	zr, err := gzip.NewReader(w2.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer zr.Close()

	var decoded []map[string]float64
	if err := json.NewDecoder(zr).Decode(&decoded); err != nil {
		t.Fatalf("decode gzip body: %v", err)
	}
	if len(decoded) != len(big) {
		t.Fatalf("decoded %d items, want %d", len(decoded), len(big))
	}
}
