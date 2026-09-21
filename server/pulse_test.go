package main

import (
	"encoding/json/v2"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testPresence(t *testing.T) *presence {
	t.Helper()
	return newPresence([]byte("test-secret"), 180*time.Second, 30*time.Second, 1000, 56)
}

// recount derives the counters from scratch. The incremental bookkeeping in
// claim/release is the only thing standing between a correct count and a
// silently wrong one, so it is checked against the slow, obvious version.
func recount(p *presence) (int, map[string]int, map[string]int) {
	world := 0
	byCountry, bySub := map[string]int{}, map[string]int{}
	for _, e := range p.entries {
		world++
		byCountry[e.country]++
		if e.sub != "" {
			bySub[e.sub]++
		}
	}
	return world, byCountry, bySub
}

func assertConsistent(t *testing.T, p *presence, step int) {
	t.Helper()
	world, byCountry, bySub := recount(p)
	if p.world != world {
		t.Fatalf("step %d: world counter %d, recount %d", step, p.world, world)
	}
	if len(p.byCountry) != len(byCountry) || len(p.bySub) != len(bySub) {
		t.Fatalf("step %d: stale zero counters: country %d/%d sub %d/%d",
			step, len(p.byCountry), len(byCountry), len(p.bySub), len(bySub))
	}
	for code, n := range byCountry {
		if p.byCountry[code] != n {
			t.Fatalf("step %d: country %s counter %d, recount %d", step, code, p.byCountry[code], n)
		}
	}
	for code, n := range bySub {
		if p.bySub[code] != n {
			t.Fatalf("step %d: sub %s counter %d, recount %d", step, code, p.bySub[code], n)
		}
	}
}

func TestCountersSurviveChurn(t *testing.T) {
	p := testPresence(t)
	countries := []string{"TR", "US", "DE"}
	subs := []string{"", "TR-55", "TR-34", "US-CA", "DE-BY"}

	rng := rand.New(rand.NewPCG(1, 2))
	now := time.Now()

	for step := range 4000 {
		now = now.Add(time.Duration(rng.IntN(40)) * time.Second)
		var addr [4]byte
		addr[0], addr[1] = 10, byte(rng.IntN(60))
		addr[2], addr[3] = byte(rng.IntN(60)), byte(rng.IntN(60))
		key := p.key(netip.AddrFrom4(addr))

		country := countries[rng.IntN(len(countries))]
		sub := subs[rng.IntN(len(subs))]
		if sub != "" && !strings.HasPrefix(sub, country) {
			sub = ""
		}
		p.beat(key, country, sub, now)
		p.sweep(now)
		assertConsistent(t, p, step)
	}

	// Everything must drain once no heartbeat arrives for longer than the TTL.
	p.sweep(now.Add(10 * time.Minute))
	if p.world != 0 || len(p.entries) != 0 || len(p.byCountry) != 0 || len(p.bySub) != 0 {
		t.Fatalf("presence did not drain: world=%d entries=%d countries=%d subs=%d",
			p.world, len(p.entries), len(p.byCountry), len(p.bySub))
	}
}

func TestKeyBucketsByPrefix(t *testing.T) {
	p := testPresence(t)
	mustAddr := func(s string) netip.Addr {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("parse %s: %v", s, err)
		}
		return a
	}

	// Two addresses inside one /56 delegation are one presence.
	if p.key(mustAddr("2a02:810d:1234:5600::1")) != p.key(mustAddr("2a02:810d:1234:56ff::abcd")) {
		t.Error("addresses in the same /56 produced different keys")
	}
	// A different delegation is a different presence.
	if p.key(mustAddr("2a02:810d:1234:5600::1")) == p.key(mustAddr("2a02:810d:1234:9900::1")) {
		t.Error("addresses in different /56s produced the same key")
	}
	// IPv4 is keyed by the whole address, so neighbours stay distinct.
	if p.key(mustAddr("203.0.113.5")) == p.key(mustAddr("203.0.113.6")) {
		t.Error("distinct IPv4 addresses produced the same key")
	}
	// The v4-mapped form of an address must not count as a second presence.
	if p.key(mustAddr("203.0.113.5")) != p.key(mustAddr("::ffff:203.0.113.5")) {
		t.Error("v4-mapped address produced a different key")
	}
}

func TestClientAddrIgnoresUntrustedForwardedHeader(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}

	cases := []struct {
		name      string
		remote    string
		forwarded string
		want      string
	}{
		{"direct hit keeps the peer", "203.0.113.9:4444", "", "203.0.113.9"},
		{"untrusted peer cannot forge a header", "203.0.113.9:4444", "8.8.8.8", "203.0.113.9"},
		{"trusted proxy supplies the client", "172.17.0.1:5000", "198.51.100.7", "198.51.100.7"},
		{"only the rightmost entry is believed", "172.17.0.1:5000", "1.2.3.4, 9.9.9.9, 198.51.100.7", "198.51.100.7"},
		{"garbage header falls back to the peer", "172.17.0.1:5000", "not-an-address", "172.17.0.1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := clientAddr(tc.remote, tc.forwarded, trusted)
			if !ok || got.String() != tc.want {
				t.Fatalf("got %v (ok=%v), want %s", got, ok, tc.want)
			}
		})
	}
}

func newTestServer(t *testing.T) *server {
	t.Helper()
	cat, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	st, err := openStore(filepath.Join(t.TempDir(), "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config{
		minBeat:       5 * time.Second,
		anomalyFloor:  20,
		anomalyFactor: 5,
		historyTTL:    30 * time.Second,
		historyMax:    64,
	}
	return &server{
		cfg:      cfg,
		catalog:  cat,
		store:    st,
		presence: newPresence([]byte("test-secret"), 180*time.Second, cfg.minBeat, 1000, 56),
		history:  newHistoryCache(cfg.historyTTL, cfg.historyMax),
		nextBeat: 60,
	}
}

func beat(t *testing.T, s *server, remote, body string) (int, heartbeatResponse) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/heartbeat", strings.NewReader(body))
	r.RemoteAddr = remote
	w := httptest.NewRecorder()
	s.handleHeartbeat(w, r)

	var out heartbeatResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v (%s)", err, w.Body)
		}
	}
	return w.Code, out
}

func TestHeartbeat(t *testing.T) {
	s := newTestServer(t)

	code, got := beat(t, s, "203.0.113.1:1000", `{"country":"TR","subdivision":"TR-55"}`)
	if code != http.StatusOK {
		t.Fatalf("first heartbeat: %d", code)
	}
	if got != (heartbeatResponse{counts{1, 1, 1}, "TR-55", 60}) {
		t.Fatalf("first heartbeat counts = %+v", got)
	}

	// A second machine on a different address is a second presence.
	if code, got = beat(t, s, "203.0.113.2:1000", `{"country":"TR","subdivision":"TR-55"}`); got.World != 2 {
		t.Fatalf("second client: code=%d counts=%+v", code, got)
	}

	// The same address beating again is the same presence, and too soon.
	if code, _ = beat(t, s, "203.0.113.1:1000", `{"country":"TR","subdivision":"TR-55"}`); code != http.StatusTooManyRequests {
		t.Fatalf("repeat heartbeat: got %d, want 429", code)
	}

	// Sharing at country granularity leaves every subdivision alone.
	if code, got = beat(t, s, "203.0.113.3:1000", `{"country":"TR"}`); got.World != 3 || got.Country != 3 || got.Subdivision != 0 {
		t.Fatalf("country-only client: code=%d counts=%+v", code, got)
	}

	// A subdivision the server does not recognise is dropped, never rejected,
	// so a client carrying an older catalog keeps being counted (SPEC §6).
	if code, got = beat(t, s, "203.0.113.4:1000", `{"country":"TR","subdivision":"TR-99"}`); code != http.StatusOK || got.SubdivisionCode != "" {
		t.Fatalf("stale subdivision: code=%d counts=%+v", code, got)
	}

	// So is a subdivision belonging to a different country.
	if code, got = beat(t, s, "203.0.113.5:1000", `{"country":"TR","subdivision":"US-CA"}`); code != http.StatusOK || got.SubdivisionCode != "" {
		t.Fatalf("mismatched subdivision: code=%d counts=%+v", code, got)
	}

	for _, body := range []string{
		`{"country":"XX"}`,
		`{"country":""}`,
		`{"country":"TR; DROP TABLE"}`,
		`not json`,
	} {
		if code, _ := beat(t, s, "203.0.113.6:1000", body); code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, code)
		}
	}
}

// The trusted-proxy rule is a trust boundary: getting it wrong either
// collapses every client into one presence or lets anyone mint unlimited ones,
// so the refusal is worth asserting.
func TestLoadConfigRefusesUnsafeSetups(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{"no trusted proxies and no opt-out", nil, true},
		{"explicit direct access", map[string]string{"ALLOW_DIRECT": "1"}, false},
		{"trusted proxy given", map[string]string{"TRUSTED_PROXIES": "172.17.0.0/16"}, false},
		{"several trusted proxies", map[string]string{"TRUSTED_PROXIES": "172.17.0.0/16, 10.0.0.0/8"}, false},
		{"not a CIDR", map[string]string{"TRUSTED_PROXIES": "172.17.0.1"}, true},
		{"garbage CIDR", map[string]string{"TRUSTED_PROXIES": "nonsense"}, true},
		{"rate limit outlives the presence", map[string]string{
			"ALLOW_DIRECT": "1", "MIN_BEAT_SECONDS": "300", "PRESENCE_TTL_SECONDS": "180",
		}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := loadConfig()
			if (err != nil) != tc.wantErr {
				t.Fatalf("loadConfig() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestHistoryRejectsUnknownScopes(t *testing.T) {
	s := newTestServer(t)

	for _, q := range []string{
		"scope=world&code=WORLD",
		"scope=country&code=TR",
		"scope=subdivision&code=TR-55",
	} {
		r := httptest.NewRequest(http.MethodGet, "/v1/history?"+q, nil)
		w := httptest.NewRecorder()
		s.handleHistory(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", q, w.Code)
		}
	}

	for _, q := range []string{
		"scope=subdivision&code=TR-99",
		"scope=country&code=WORLD",
		"scope=world&code=TR",
		"scope=planet&code=WORLD",
		"scope=subdivision&code=%27%20OR%201%3D1",
	} {
		r := httptest.NewRequest(http.MethodGet, "/v1/history?"+q, nil)
		w := httptest.NewRecorder()
		s.handleHistory(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", q, w.Code)
		}
	}
}

func TestSnapshotSkipsEmptyScopes(t *testing.T) {
	s := newTestServer(t)
	beat(t, s, "203.0.113.1:1000", `{"country":"TR","subdivision":"TR-55"}`)
	beat(t, s, "203.0.113.2:1000", `{"country":"DE"}`)

	rows := s.presence.snapshot()
	// world + TR + DE + TR-55, and nothing for the ~3800 empty scopes.
	if len(rows) != 4 {
		t.Fatalf("snapshot wrote %d rows, want 4: %+v", len(rows), rows)
	}

	ts := time.Now().Unix()
	if err := s.store.writeSnapshot(ts, rows); err != nil {
		t.Fatal(err)
	}
	points, err := s.store.points("subdivision", "TR-55", ts-60)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0][1] != 1 {
		t.Fatalf("points = %+v", points)
	}
}

// One address is one presence holding one location. Answering "can an attacker
// claim every city at once, or flip between them fast enough to be in two
// places" structurally rather than by policy: the map key is the identity, the
// entry holds a single location, and release/claim are paired.
func TestOnePresenceCannotOccupyTwoScopes(t *testing.T) {
	p := testPresence(t)
	key := p.key(netip.MustParseAddr("203.0.113.7"))
	now := time.Now()

	tour := []struct{ country, sub string }{
		{"TR", "TR-55"}, {"TR", "TR-34"}, {"TR", "TR-06"},
		{"US", "US-CA"}, {"DE", "DE-BY"}, {"JP", "JP-13"},
		{"TR", "TR-55"}, {"TR", ""},
	}

	for i, stop := range tour {
		now = now.Add(p.minBeat)
		got, err := p.beat(key, stop.country, stop.sub, now)
		if err != nil {
			t.Fatalf("stop %d: %v", i, err)
		}
		if got.World != 1 {
			t.Fatalf("stop %d (%s/%s): world = %d, want 1", i, stop.country, stop.sub, got.World)
		}

		p.mu.Lock()
		// Exactly one country and at most one subdivision may be non-zero,
		// and each at exactly one.
		if len(p.byCountry) != 1 || p.byCountry[stop.country] != 1 {
			p.mu.Unlock()
			t.Fatalf("stop %d: country counters = %v, want only %s=1", i, p.byCountry, stop.country)
		}
		wantSubs := 1
		if stop.sub == "" {
			wantSubs = 0
		}
		if len(p.bySub) != wantSubs || (stop.sub != "" && p.bySub[stop.sub] != 1) {
			p.mu.Unlock()
			t.Fatalf("stop %d: subdivision counters = %v, want only %q", i, p.bySub, stop.sub)
		}
		p.mu.Unlock()
	}

	// Flipping faster than the minimum interval does not move the presence at
	// all, so the previous location keeps the count rather than both holding it.
	if _, err := p.beat(key, "US", "US-NY", now); !errors.Is(err, errRateLimited) {
		t.Fatalf("immediate second location change: err = %v, want rate limited", err)
	}
	p.mu.Lock()
	stillThere := len(p.byCountry) == 1 && p.byCountry["TR"] == 1
	p.mu.Unlock()
	if !stillThere {
		t.Fatal("a rate-limited location change disturbed the counters")
	}
}

// The whole read-modify-write is under one lock, so concurrent beats from one
// address cannot both claim before either releases. Worth asserting under
// -race, since a torn update here would be an inflation bug.
func TestConcurrentBeatsKeepCountersHonest(t *testing.T) {
	p := testPresence(t)
	p.minBeat = 0 // let every concurrent attempt through to the counters

	addrs := make([]netip.Addr, 16)
	for i := range addrs {
		addrs[i] = netip.AddrFrom4([4]byte{203, 0, 113, byte(i)})
	}
	places := []struct{ country, sub string }{
		{"TR", "TR-55"}, {"TR", "TR-34"}, {"US", "US-CA"}, {"DE", ""},
	}

	var wg sync.WaitGroup
	now := time.Now()
	for i := range addrs {
		for round := range 50 {
			wg.Go(func() {
				place := places[(i+round)%len(places)]
				p.beat(p.key(addrs[i]), place.country, place.sub, now)
			})
		}
	}
	wg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()
	world, byCountry, bySub := recount(p)
	if p.world != world || p.world != len(addrs) {
		t.Fatalf("world counter %d, recount %d, distinct addresses %d", p.world, world, len(addrs))
	}
	for code, n := range byCountry {
		if p.byCountry[code] != n {
			t.Fatalf("country %s counter %d, recount %d", code, p.byCountry[code], n)
		}
	}
	for code, n := range bySub {
		if p.bySub[code] != n {
			t.Fatalf("subdivision %s counter %d, recount %d", code, p.bySub[code], n)
		}
	}
	if len(p.byCountry) != len(byCountry) || len(p.bySub) != len(bySub) {
		t.Fatalf("stale zero counters: country %d/%d sub %d/%d",
			len(p.byCountry), len(byCountry), len(p.bySub), len(bySub))
	}
}

// The history endpoint is the cheapest thing to flood — a range scan plus an
// aggregate, on a deliberately single SQLite connection — so repeated reads
// must not reach the database. Proven by changing the stored data underneath a
// live cache entry and asserting the response does not move.
func TestHistoryIsServedFromCache(t *testing.T) {
	s := newTestServer(t)
	ts := time.Now().Unix()

	ask := func() (int, string, string) {
		r := httptest.NewRequest(http.MethodGet, "/v1/history?scope=country&code=TR", nil)
		w := httptest.NewRecorder()
		s.handleHistory(w, r)
		return w.Code, w.Header().Get("ETag"), w.Body.String()
	}

	if err := s.store.writeSnapshot(ts, []scopeCount{{Scope: "country", Code: "TR", Online: 3}}); err != nil {
		t.Fatal(err)
	}
	code, etag, first := ask()
	if code != http.StatusOK || !strings.Contains(first, `"today_peak":3`) {
		t.Fatalf("first read: code=%d body=%s", code, first)
	}

	if err := s.store.writeSnapshot(ts+300, []scopeCount{{Scope: "country", Code: "TR", Online: 99}}); err != nil {
		t.Fatal(err)
	}
	if _, cachedEtag, cached := ask(); cached != first || cachedEtag != etag {
		t.Fatalf("second read hit the database: etag %q->%q", etag, cachedEtag)
	}

	// An expired entry does go back to the database.
	s.history = newHistoryCache(0, 64)
	if _, _, fresh := ask(); !strings.Contains(fresh, `"today_peak":99`) {
		t.Fatalf("expired cache did not refresh: %s", fresh)
	}

	// A conditional request against a live cache entry is answered without a
	// body, so a client that already has the data costs no bandwidth either.
	s.history = newHistoryCache(time.Minute, 64)
	s.history.put("country/TR/24h", []byte(first), etag, time.Now())
	r := httptest.NewRequest(http.MethodGet, "/v1/history?scope=country&code=TR", nil)
	r.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	s.handleHistory(w, r)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("conditional request: code=%d bytes=%d", w.Code, w.Body.Len())
	}

	// The cap bounds a request pattern that walks every scope.
	capped := newHistoryCache(time.Minute, 4)
	for i := range 20 {
		capped.put("k"+strconv.Itoa(i), []byte("x"), "e", time.Now())
	}
	if len(capped.entries) > 4 {
		t.Fatalf("cache grew to %d entries, cap is 4", len(capped.entries))
	}
}
