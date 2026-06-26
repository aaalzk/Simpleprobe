package server

import (
	"sync"
	"time"
)

// GazeTracker tracks whether someone is actively viewing the web dashboard.
// When a gaze ping is received, gaze mode activates. If no ping arrives
// within the timeout period, gaze mode deactivates.
type GazeTracker struct {
	enabled bool
	mu      sync.Mutex
	lastPing time.Time
	timeout  time.Duration
}

// NewGazeTracker creates a new GazeTracker.
func NewGazeTracker(enabled bool, timeout time.Duration) *GazeTracker {
	return &GazeTracker{
		enabled: enabled,
		timeout: timeout,
	}
}

// Ping records a gaze signal. Called when a web client fetches /api/servers.
func (g *GazeTracker) Ping() {
	if !g.enabled {
		return
	}
	g.mu.Lock()
	g.lastPing = time.Now()
	g.mu.Unlock()
}

// IsActive returns true if a gaze ping was received within the timeout window.
func (g *GazeTracker) IsActive() bool {
	if !g.enabled {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Since(g.lastPing) < g.timeout
}

// Start begins the background cleanup loop.
func (g *GazeTracker) Start() {
	if !g.enabled {
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			// IsActive checks the timestamp; no action needed here.
			// The state is queried on-demand when building API responses.
			_ = g.IsActive()
		}
	}()
}
