package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Pusher sends metrics reports to the server.
type Pusher struct {
	serverURL string
	token     string
	client    *http.Client
	userAgent string
}

// NewPusher creates a new Pusher.
func NewPusher(serverURL, token, version string) *Pusher {
	return &Pusher{
		serverURL: serverURL,
		token:     token,
		userAgent: "sysprobe-agent/" + version,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Push sends a report to the server. Returns an error if the request fails
// or the server returns a non-2xx status.
func (p *Pusher) Push(report Report) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	url := p.serverURL + "/api/report"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(msg))
	}
	return nil
}