package api

import (
	"strings"
	"sync"
	"time"
)

// accountGuard limits password guessing against one account from many
// addresses, which the per-address limit cannot see.
//
// Locking an account outright would let anyone lock the operator out of
// their own monitoring by failing on purpose. So once an account has had too
// many recent failures, it only refuses addresses that have not signed into
// it before; wherever the owner normally signs in from keeps working.
type accountGuard struct {
	mu        sync.Mutex
	window    time.Duration
	max       int
	knownFor  time.Duration
	failures  map[string][]time.Time
	known     map[string]map[string]time.Time
	lastSweep time.Time
	now       func() time.Time
}

func newAccountGuard(window time.Duration, max int, knownFor time.Duration) *accountGuard {
	return &accountGuard{
		window: window, max: max, knownFor: knownFor,
		failures: map[string][]time.Time{},
		known:    map[string]map[string]time.Time{},
		now:      time.Now,
	}
}

// accountKey normalises an email the way login matches it, and bounds the
// size of a key an attacker chooses.
func accountKey(email string) string {
	k := strings.ToLower(strings.TrimSpace(email))
	if len(k) > 254 {
		k = k[:254]
	}
	return k
}

// blocked reports whether a login for account from addr should be refused.
func (g *accountGuard) blocked(account, addr string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	g.sweep(now)
	if g.recentFailures(account, now) < g.max {
		return false
	}
	seen, ok := g.known[account][addr]
	return !ok || now.Sub(seen) > g.knownFor
}

func (g *accountGuard) fail(account string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	g.failures[account] = append(g.failures[account], now)
	// Only the last max failures matter; keep the slice from growing.
	if n := len(g.failures[account]); n > g.max {
		g.failures[account] = g.failures[account][n-g.max:]
	}
}

func (g *accountGuard) success(account, addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.failures, account)
	if g.known[account] == nil {
		g.known[account] = map[string]time.Time{}
	}
	g.known[account][addr] = g.now()
}

func (g *accountGuard) recentFailures(account string, now time.Time) int {
	cutoff := now.Add(-g.window)
	n := 0
	for _, t := range g.failures[account] {
		if t.After(cutoff) {
			n++
		}
	}
	return n
}

// sweep forgets quiet accounts and stale addresses, at most once a window.
func (g *accountGuard) sweep(now time.Time) {
	if now.Sub(g.lastSweep) < g.window {
		return
	}
	g.lastSweep = now
	for account := range g.failures {
		if g.recentFailures(account, now) == 0 {
			delete(g.failures, account)
		}
	}
	for account, addrs := range g.known {
		for addr, seen := range addrs {
			if now.Sub(seen) > g.knownFor {
				delete(addrs, addr)
			}
		}
		if len(addrs) == 0 {
			delete(g.known, account)
		}
	}
}
