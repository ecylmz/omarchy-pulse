package main

import (
	"sync"
	"time"
)

// historyCache collapses repeated history reads into one SQLite query per
// scope per TTL.
//
// The endpoint is the cheapest thing to flood: every request is a range scan
// plus an aggregate, and the database is deliberately held on a single
// connection (store.go), so concurrent reads serialise. Since the underlying
// data only changes when a snapshot is written every five minutes, answering
// from memory costs nothing in freshness and removes the amplification.
type historyCache struct {
	ttl time.Duration
	max int

	mu      sync.Mutex
	entries map[string]cachedResponse
}

type cachedResponse struct {
	body    []byte
	etag    string
	expires time.Time
}

func newHistoryCache(ttl time.Duration, max int) *historyCache {
	return &historyCache{ttl: ttl, max: max, entries: make(map[string]cachedResponse)}
}

func (c *historyCache) get(key string, now time.Time) (cachedResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok || now.After(entry.expires) {
		return cachedResponse{}, false
	}
	return entry, true
}

func (c *historyCache) put(key string, body []byte, etag string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// ponytail: drop everything rather than track an eviction order. The key
	// space is bounded by the catalog (~3800 scopes) and entries are a few
	// hundred bytes, so the cap only exists to bound a request pattern that
	// walks every scope; reaching it is already anomalous.
	if len(c.entries) >= c.max {
		clear(c.entries)
	}
	c.entries[key] = cachedResponse{body: body, etag: etag, expires: now.Add(c.ttl)}
}
