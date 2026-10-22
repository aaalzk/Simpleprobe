package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ReportResponse is the response from the server after pushing a report.
type ReportResponse struct {
	Status string `json:"status"`
	Gaze   bool   `json:"gaze"`
}

// maxResponseBody bounds how much of a response body is read. A normal
// response is a tiny JSON object; the limit only exists so that an error
// page cannot be downloaded without bounds.
const maxResponseBody = 4096

// Pusher sends metrics reports to the server.
type Pusher struct {
	serverURL string
	token     string
	client    *http.Client
	userAgent string
	lastGaze  bool
}

// NewPusher creates a new Pusher.
//
// contact is an optional contact URL or e-mail that is appended to the
// User-Agent, e.g. "simpleprobe-agent/1.5.0 (https://example.com/agent)".
// Bot-management rules treat an identifiable, contactable client far more
// leniently than an anonymous one, and the contact address is what a
// Cloudflare administrator looks for when deciding whether to allow the
// traffic.
func NewPusher(serverURL, token, version, contact string) *Pusher {
	ua := "simpleprobe-agent/" + version
	if c := strings.TrimSpace(contact); c != "" {
		ua += " (" + c + ")"
	}
	return &Pusher{
		serverURL: serverURL,
		token:     token,
		userAgent: ua,
		client: &http.Client{
			Timeout:   15 * time.Second,
			Transport: newTransport(),
		},
	}
}

// newTransport builds the transport used for reports. Connections are reused
// so a report does not pay for a fresh TLS handshake every time, and the
// proxy setting keeps the HTTP_PROXY / HTTPS_PROXY environment variables
// working.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// Push sends a report to the server. Returns the server response including gaze status.
func (p *Pusher) Push(report Report) (ReportResponse, error) {
	var respData ReportResponse

	body, err := json.Marshal(report)
	if err != nil {
		return respData, fmt.Errorf("marshal report: %w", err)
	}

	url := p.serverURL + "/api/report"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return respData, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return respData, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return respData, describeResponseError(resp, respBody)
	}

	// Parse response to get gaze status
	if err := json.Unmarshal(respBody, &respData); err != nil {
		if looksLikeHTML(respBody) {
			return respData, fmt.Errorf("server returned %d with an HTML page instead of JSON (likely a Cloudflare challenge or error page): %s",
				resp.StatusCode, cfHint(resp))
		}
		return respData, fmt.Errorf("decode server response: %w", err)
	}
	p.lastGaze = respData.Gaze

	return respData, nil
}

// describeResponseError turns a non-2xx response into an actionable error.
//
// Cloudflare's edge answers with its own error pages (1015 rate limit, 1020
// firewall block, 520/522/524 origin errors), so telling them apart from a
// genuine error of our own server is the difference between "adjust a rule
// in the Cloudflare dashboard" and "debug the server".
func describeResponseError(resp *http.Response, body []byte) error {
	ray := resp.Header.Get("Cf-Ray")
	mitigated := resp.Header.Get("Cf-Mitigated")
	viaCloudflare := ray != "" || mitigated != "" ||
		strings.Contains(strings.ToLower(resp.Header.Get("Server")), "cloudflare")

	snippet := strings.TrimSpace(string(body))
	snippet = strings.ReplaceAll(snippet, "\r", " ")
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}

	if !viaCloudflare {
		if snippet == "" {
			snippet = resp.Status
		}
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, snippet)
	}

	reason := "request was blocked by Cloudflare"
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		reason = "Cloudflare rate limit (error 1015): the zone's rate limiting rule matched this request"
	case resp.StatusCode == http.StatusForbidden && mitigated != "":
		reason = "Cloudflare challenge / bot mitigation (" + mitigated + "): the request was challenged instead of reaching the server"
	case resp.StatusCode == http.StatusForbidden:
		reason = "Cloudflare firewall or WAF rule blocked the request (error 1020)"
	case resp.StatusCode == 520:
		reason = "Cloudflare could not reach the origin (error 520)"
	case resp.StatusCode == 522:
		reason = "Cloudflare could not connect to the origin (error 522)"
	case resp.StatusCode == 524:
		reason = "the origin did not answer within Cloudflare's timeout (error 524)"
	case resp.StatusCode >= 500:
		reason = "Cloudflare edge error"
	}

	msg := fmt.Sprintf("%s — HTTP %d", reason, resp.StatusCode)
	if ray != "" {
		msg += " (cf-ray " + ray + ")"
	}
	if snippet != "" {
		msg += ": " + snippet
	}
	return errors.New(msg)
}

// cfHint returns a short hint about the Cloudflare edge that handled the
// request, so the operator can look it up in the Cloudflare logs.
func cfHint(resp *http.Response) string {
	if ray := resp.Header.Get("Cf-Ray"); ray != "" {
		return "cf-ray " + ray
	}
	if s := resp.Header.Get("Server"); s != "" {
		return "server: " + s
	}
	return "no Cloudflare headers present"
}

// looksLikeHTML reports whether a body looks like an HTML document, which is
// how Cloudflare's challenge and error pages arrive.
func looksLikeHTML(body []byte) bool {
	head := body
	if len(head) > 64 {
		head = head[:64]
	}
	s := strings.ToLower(strings.TrimSpace(string(head)))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html")
}

// LastGaze returns the gaze status from the most recent push.
func (p *Pusher) LastGaze() bool {
	return p.lastGaze
}
