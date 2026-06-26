package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ReportResponse is the response from the server after pushing a report.
type ReportResponse struct {
	Status string `json:"status"`
	Gaze   bool   `json:"gaze"`
}

// Pusher sends metrics reports to the server.
type Pusher struct {
	serverURL string
	token     string
	client    *http.Client
	userAgent string
	lastGaze  bool
}

// NewPusher creates a new Pusher.
func NewPusher(serverURL, token, version string) *Pusher {
	return &Pusher{
		serverURL: serverURL,
		token:     token,
		userAgent: "simpleprobe-agent/" + version,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
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
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return respData, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return respData, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(msg))
	}

	// Parse response to get gaze status
	if err := json.NewDecoder(resp.Body).Decode(&respData); err == nil {
		p.lastGaze = respData.Gaze
	}

	return respData, nil
}

// LastGaze returns the gaze status from the most recent push.
func (p *Pusher) LastGaze() bool {
	return p.lastGaze
}