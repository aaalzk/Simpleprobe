package server

import (
	"net/http"
	"strings"
)

// isCloudflareChallenge reports whether the response is a Cloudflare
// interstitial (bot challenge, managed challenge or rate limit) instead of
// the site's own answer.
//
// Such a response says nothing about the site's availability: our probe was
// challenged, not the site. Reporting it as "down" would raise a false
// alarm, so the probe records it as a separate "challenged" state that
// neither confirms nor denies an outage.
func isCloudflareChallenge(resp *http.Response, body []byte) bool {
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusServiceUnavailable, http.StatusTooManyRequests:
	default:
		return false
	}

	// Cloudflare announces a mitigated request with this header.
	if resp.Header.Get("Cf-Mitigated") != "" {
		return true
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Server")), "cloudflare") {
		return false
	}

	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "cf-chl-") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "just a moment") ||
		strings.Contains(lower, "cf-mitigated")
}
