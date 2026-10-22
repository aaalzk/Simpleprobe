package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	// sseHeartbeat is how often a heartbeat comment is written to an idle
	// stream. Cloudflare's proxy read timeout towards the origin is 125
	// seconds, so a quiet connection would be dropped with a 524 unless we
	// keep it alive.
	sseHeartbeat = 15 * time.Second

	// sseWriteTimeout is the write deadline that is refreshed before every
	// write to a stream. The HTTP server sets a 15 second WriteTimeout,
	// which would otherwise close the stream right after the first event.
	sseWriteTimeout = 30 * time.Second
)

// Hub fans out "something changed" notifications to the connected SSE
// clients.
//
// Every subscriber gets a buffered channel of size one, so a burst of
// reports collapses into a single notification instead of one write per
// report.
type Hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[chan struct{}]struct{})}
}

// Subscribe registers a subscriber and returns its notification channel
// together with an unsubscribe function.
func (h *Hub) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Notify tells every subscriber that the state changed. It never blocks:
// a subscriber that is still busy with the previous notification simply
// keeps one pending notification.
func (h *Hub) Notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// HandleStream serves the dashboard event stream (Server-Sent Events).
//
// The current server list is pushed whenever a report arrives, so the
// dashboard no longer has to poll /api/servers every few seconds. The
// response is streamed with chunked encoding, never cached and never
// compressed; a heartbeat comment keeps the connection alive through the
// proxies that would otherwise time it out.
func (h *APIHandler) HandleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Ask nginx not to buffer the response. The Connection header is left
	// to the HTTP stack: keep-alive is the default for HTTP/1.1 and the
	// header is forbidden in HTTP/2.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// The open connection is the gaze signal: somebody is watching.
	h.gaze.Ping()

	notify, unsubscribe := h.hub.Subscribe()
	defer unsubscribe()

	rc := http.NewResponseController(w)
	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()

	writeEvent := func(event string, data []byte) bool {
		// Refresh the write deadline; the server wide WriteTimeout would
		// otherwise kill a long lived stream.
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Send the current state right away so the dashboard paints without
	// waiting for the first report.
	if !h.sendState(writeEvent) {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-notify:
			if !h.sendState(writeEvent) {
				return
			}
		case <-ticker.C:
			h.gaze.Ping()
			// A comment line is ignored by the client but keeps the
			// connection (and every proxy in between) alive.
			_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// sendState writes the current server list as an SSE event. A transient
// store error keeps the stream alive instead of tearing it down.
func (h *APIHandler) sendState(writeEvent func(string, []byte) bool) bool {
	servers, err := h.store.GetAllServers()
	if err != nil {
		log.Printf("ERROR: get servers for stream: %v", err)
		return true
	}
	if servers == nil {
		servers = []ServerRecord{}
	}

	payload, err := json.Marshal(struct {
		Servers []ServerRecord `json:"servers"`
		Gaze    bool           `json:"gaze"`
	}{
		Servers: servers,
		Gaze:    h.gaze.IsActive(),
	})
	if err != nil {
		log.Printf("ERROR: marshal stream state: %v", err)
		return true
	}
	return writeEvent("state", payload)
}
