package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"maps"
	"net/netip"
	"sync"
	"time"
)

// presenceKey is HMAC(bootSecret, ip-prefix) truncated to 16 bytes. It exists
// only as a map key with a TTL: nothing is derived from it and nothing is
// persisted from it (SPEC §5).
type presenceKey [16]byte

var (
	errRateLimited = errors.New("rate limited")
	errAtCapacity  = errors.New("at capacity")
)

type entry struct {
	country string
	sub     string
	expires time.Time
	last    time.Time
}

type counts struct {
	World       int `json:"world"`
	Country     int `json:"country"`
	Subdivision int `json:"subdivision"`
}

type scopeCount struct {
	Scope  string
	Code   string
	Online int
}

type presence struct {
	secret  []byte
	ttl     time.Duration
	minBeat time.Duration
	maxKeys int
	v6Bits  int

	// Counters are maintained incrementally so a heartbeat is O(1) rather
	// than a scan of every live presence. recount() in the tests proves they
	// stay in step with entries.
	mu        sync.Mutex
	entries   map[presenceKey]entry
	world     int
	byCountry map[string]int
	bySub     map[string]int
}

func newPresence(secret []byte, ttl, minBeat time.Duration, maxKeys, v6Bits int) *presence {
	return &presence{
		secret:    secret,
		ttl:       ttl,
		minBeat:   minBeat,
		maxKeys:   maxKeys,
		v6Bits:    v6Bits,
		entries:   make(map[presenceKey]entry),
		byCountry: make(map[string]int),
		bySub:     make(map[string]int),
	}
}

// key buckets an address by prefix: the whole address for IPv4, v6Bits for
// IPv6 so that one subscriber delegation is one presence rather than 2^72 of
// them (SPEC §5).
func (p *presence) key(addr netip.Addr) presenceKey {
	addr = addr.Unmap()
	bits := addr.BitLen()
	if addr.Is6() {
		bits = min(p.v6Bits, bits)
	}
	masked := addr
	if pfx, err := addr.Prefix(bits); err == nil {
		masked = pfx.Addr()
	}
	mac := hmac.New(sha256.New, p.secret)
	mac.Write(masked.AsSlice())
	mac.Write([]byte{byte(bits)})
	return presenceKey(mac.Sum(nil)[:16])
}

func (p *presence) beat(k presenceKey, country, sub string, now time.Time) (counts, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	prev, seen := p.entries[k]
	switch {
	case seen && now.Sub(prev.last) < p.minBeat:
		return counts{}, errRateLimited
	case !seen && len(p.entries) >= p.maxKeys:
		return counts{}, errAtCapacity
	}
	if seen {
		p.release(prev)
	}
	p.claim(country, sub)
	p.entries[k] = entry{country: country, sub: sub, expires: now.Add(p.ttl), last: now}

	return counts{World: p.world, Country: p.byCountry[country], Subdivision: p.bySub[sub]}, nil
}

func (p *presence) claim(country, sub string) {
	p.world++
	p.byCountry[country]++
	if sub != "" {
		p.bySub[sub]++
	}
}

// release drops a presence's contribution, deleting counters that reach zero
// so the maps stay proportional to live usage rather than to every scope ever
// seen.
func (p *presence) release(e entry) {
	p.world--
	decr(p.byCountry, e.country)
	if e.sub != "" {
		decr(p.bySub, e.sub)
	}
}

func decr(m map[string]int, code string) {
	if m[code] <= 1 {
		delete(m, code)
		return
	}
	m[code]--
}

// sweep drops presences whose TTL has passed. This is the only way a presence
// ever disappears: there is no "goodbye" call, so pausing, suspending and
// unplugging the network all behave identically (SPEC §13.3).
func (p *presence) sweep(now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	before := len(p.entries)
	maps.DeleteFunc(p.entries, func(_ presenceKey, e entry) bool {
		if now.Before(e.expires) {
			return false
		}
		p.release(e)
		return true
	})
	return before - len(p.entries)
}

func (p *presence) counts(country, sub string) counts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return counts{World: p.world, Country: p.byCountry[country], Subdivision: p.bySub[sub]}
}

// snapshot returns every scope with at least one presence. Scopes at zero are
// omitted deliberately: writing all ~3800 catalog scopes every five minutes
// would be ~1.2M rows a day of almost entirely zeros (SPEC §15.2).
func (p *presence) snapshot() []scopeCount {
	p.mu.Lock()
	defer p.mu.Unlock()

	rows := make([]scopeCount, 0, 1+len(p.byCountry)+len(p.bySub))
	if p.world > 0 {
		rows = append(rows, scopeCount{Scope: "world", Code: "WORLD", Online: p.world})
	}
	for code, n := range p.byCountry {
		rows = append(rows, scopeCount{Scope: "country", Code: code, Online: n})
	}
	for code, n := range p.bySub {
		rows = append(rows, scopeCount{Scope: "subdivision", Code: code, Online: n})
	}
	return rows
}
