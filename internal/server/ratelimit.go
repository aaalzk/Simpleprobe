package server

import (
	"sync"
	"time"
)

type rateEntry struct {
	count      int
	firstSeen  time.Time
	blocked    bool
	blockUntil time.Time
}

// RateLimiter tracks failed auth attempts per IP and blocks offenders.
type RateLimiter struct {
	mu       sync.Mutex
	entries  map[string]*rateEntry
	maxFails int
	window   time.Duration
	banTime  time.Duration
	onAlert  func(ip string, count int)
}

// NewRateLimiter creates a RateLimiter.
func NewRateLimiter(maxFails int, window, banTime time.Duration) *RateLimiter {
	return &RateLimiter{
		entries:  make(map[string]*rateEntry),
		maxFails: maxFails,
		window:   window,
		banTime:  banTime,
	}
}

// OnAlert sets a callback when an IP exceeds the threshold.
func (rl *RateLimiter) OnAlert(fn func(ip string, count int)) {
	rl.onAlert = fn
}

// Allow returns false if the IP is currently blocked.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	e, exists := rl.entries[ip]
	if !exists {
		rl.entries[ip] = &rateEntry{count: 0, firstSeen: now}
		return true
	}
	if e.blocked {
		if now.Before(e.blockUntil) {
			return false
		}
		e.blocked = false
		e.count = 0
		e.firstSeen = now
		return true
	}
	if now.Sub(e.firstSeen) > rl.window {
		e.count = 0
		e.firstSeen = now
	}
	return true
}

// RecordFailure records a failed attempt. Returns true if the IP was just blocked.
func (rl *RateLimiter) RecordFailure(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	e, exists := rl.entries[ip]
	if !exists {
		rl.entries[ip] = &rateEntry{count: 1, firstSeen: now}
		return false
	}
	if now.Sub(e.firstSeen) > rl.window {
		e.count = 0
		e.firstSeen = now
	}
	e.count++
	if e.count >= rl.maxFails && !e.blocked {
		e.blocked = true
		e.blockUntil = now.Add(rl.banTime)
		if rl.onAlert != nil {
			rl.onAlert(ip, e.count)
		}
		return true
	}
	return false
}

// Cleanup removes expired entries.
func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, e := range rl.entries {
		if !e.blocked && now.Sub(e.firstSeen) > rl.window*2 {
			delete(rl.entries, ip)
		}
		if e.blocked && now.Sub(e.blockUntil) > rl.banTime {
			delete(rl.entries, ip)
		}
	}
}