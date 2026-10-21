package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"argus/internal/store"
)

type rateLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	max       int
	attempts  map[string][]time.Time
	lastSweep time.Time
}

func newRateLimiter(window time.Duration, max int) *rateLimiter {
	return &rateLimiter{window: window, max: max, attempts: map[string][]time.Time{}, lastSweep: time.Now()}
}

func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now, cutoff)
	hits := l.attempts[key]
	n := 0
	for _, t := range hits {
		if t.After(cutoff) {
			hits[n] = t
			n++
		}
	}
	hits = hits[:n]
	if len(hits) >= l.max {
		l.attempts[key] = hits
		return false
	}
	l.attempts[key] = append(hits, now)
	return true
}

// sweep drops keys with no attempts left in the window, at most once per
// window. Keys come from callers' addresses, so without it every address that
// ever knocked would stay in memory.
func (l *rateLimiter) sweep(now, cutoff time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for key, hits := range l.attempts {
		if len(hits) == 0 || !hits[len(hits)-1].After(cutoff) {
			delete(l.attempts, key)
		}
	}
}

func (l *rateLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

type statusCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]statusCacheEntry
}

type statusCacheEntry struct {
	at  time.Time
	pub store.PublicStatus
}

func newStatusCache(ttl time.Duration) *statusCache {
	return &statusCache{ttl: ttl, entries: map[string]statusCacheEntry{}}
}

func (c *statusCache) get(slug string) (store.PublicStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[slug]
	if !ok || time.Since(e.at) > c.ttl {
		delete(c.entries, slug)
		return store.PublicStatus{}, false
	}
	return e.pub, true
}

func (c *statusCache) put(slug string, pub store.PublicStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[slug] = statusCacheEntry{at: time.Now(), pub: pub}
}

func (c *statusCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]statusCacheEntry{}
}

// clientIPFrom finds the caller's address given how many reverse proxies sit
// in front of the API. Each proxy appends the address it received the request
// from, so the chain reads client, proxy, proxy, ... and ends with the peer
// that connected to us. Only the last `proxies` hops were written by proxies
// Argus trusts; anything left of them was supplied by the caller and can say
// anything, which is why the leftmost entry is never believed on its own.
func clientIPFrom(r *http.Request, proxies int) string {
	peer := remoteHost(r)
	if proxies <= 0 {
		return peer
	}
	var chain []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				chain = append(chain, p)
			}
		}
	}
	chain = append(chain, peer)
	i := len(chain) - 1 - proxies
	if i < 0 {
		// Fewer hops than configured proxies: the request did not come
		// through all of them, so the first address is the best there is.
		i = 0
	}
	if addr, ok := parseHop(chain[i]); ok {
		return addr
	}
	return peer
}

// parseHop accepts an address with or without a port, as proxies differ.
func parseHop(hop string) (string, bool) {
	if a, err := netip.ParseAddr(hop); err == nil {
		return a.Unmap().String(), true
	}
	if ap, err := netip.ParseAddrPort(hop); err == nil {
		return ap.Addr().Unmap().String(), true
	}
	return "", false
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return a.Unmap().String()
	}
	return host
}

// limitKey is the address a rate limit counts against. An IPv6 client is
// normally handed a whole /64, so counting single addresses would let one
// caller rotate through billions of them.
func limitKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Is4() {
		return ip
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}

// clientIP is the caller's address as recorded in the audit log.
func (s *Server) clientIP(r *http.Request) string {
	return clientIPFrom(r, s.cfg.TrustedProxies)
}

// rateKey is what the per-caller rate limits count against.
func (s *Server) rateKey(r *http.Request) string {
	return limitKey(s.clientIP(r))
}
