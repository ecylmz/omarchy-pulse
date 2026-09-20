// Command pulse serves ambient presence counts for the Omarchy ecosystem.
//
// Live presence lives in memory keyed by a hash of the client's address
// prefix; SQLite holds aggregate history only. See SPEC.md.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type config struct {
	addr          string
	dbPath        string
	trusted       []netip.Prefix
	heartbeat     time.Duration
	ttl           time.Duration
	minBeat       time.Duration
	snapshotEvery time.Duration
	sweepEvery    time.Duration
	maxKeys       int
	v6Bits        int
	anomalyFloor  int
	anomalyFactor int
}

func env(key, def string) string { return cmp.Or(os.Getenv(key), def) }

func envSeconds(key string, def int) time.Duration {
	n, err := strconv.Atoi(env(key, strconv.Itoa(def)))
	if err != nil || n <= 0 {
		n = def
	}
	return time.Duration(n) * time.Second
}

func envInt(key string, def int) int {
	n, err := strconv.Atoi(env(key, strconv.Itoa(def)))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func loadConfig() (config, error) {
	c := config{
		addr:          ":" + env("PORT", "5000"),
		dbPath:        env("DB_PATH", "/data/pulse.db"),
		heartbeat:     envSeconds("HEARTBEAT_SECONDS", 60),
		ttl:           envSeconds("PRESENCE_TTL_SECONDS", 180),
		minBeat:       envSeconds("MIN_BEAT_SECONDS", 30),
		snapshotEvery: envSeconds("SNAPSHOT_SECONDS", 300),
		sweepEvery:    envSeconds("SWEEP_SECONDS", 5),
		maxKeys:       envInt("MAX_KEYS", 200_000),
		v6Bits:        envInt("IPV6_PREFIX_BITS", 56),
		anomalyFloor:  envInt("ANOMALY_FLOOR", 20),
		anomalyFactor: envInt("ANOMALY_FACTOR", 5),
	}

	for _, raw := range strings.Split(env("TRUSTED_PROXIES", ""), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		pfx, err := netip.ParsePrefix(raw)
		if err != nil {
			return c, fmt.Errorf("TRUSTED_PROXIES: %q is not a CIDR: %w", raw, err)
		}
		c.trusted = append(c.trusted, pfx)
	}

	// SPEC §5.3. Behind a proxy with no trusted list, every client would
	// collapse into the proxy's single presence key — and if a forwarded
	// header were trusted blindly instead, anyone could mint unlimited keys.
	// Refuse rather than guess which mistake is being made.
	if len(c.trusted) == 0 && os.Getenv("ALLOW_DIRECT") != "1" {
		return c, errors.New(
			"TRUSTED_PROXIES is empty: set it to the reverse proxy's CIDRs, " +
				"or set ALLOW_DIRECT=1 if clients really do connect directly")
	}
	return c, nil
}

type server struct {
	cfg      config
	catalog  *catalog
	presence *presence
	store    *store
	nextBeat int
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	cat, err := loadCatalog()
	if err != nil {
		return err
	}
	st, err := openStore(cfg.dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// The boot secret never leaves memory and is never persisted, so a
	// restart invalidates every presence key. The map refills within one
	// heartbeat interval (SPEC §5).
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("generate boot secret: %w", err)
	}

	s := &server{
		cfg:      cfg,
		catalog:  cat,
		presence: newPresence(secret, cfg.ttl, cfg.minBeat, cfg.maxKeys, cfg.v6Bits),
		store:    st,
		nextBeat: int(cfg.heartbeat.Seconds()),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("GET /v1/history", s.handleHistory)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	httpSrv := &http.Server{
		Addr:              cfg.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Go(func() { s.loop(ctx, cfg.sweepEvery, func() { s.presence.sweep(time.Now()) }) })
	wg.Go(func() { s.loop(ctx, cfg.snapshotEvery, s.snapshot) })

	wg.Go(func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdown)
	})

	slog.Info("listening", "addr", cfg.addr, "db", cfg.dbPath,
		"trusted_proxies", len(cfg.trusted), "ttl", cfg.ttl, "heartbeat", cfg.heartbeat)

	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		stop()
		wg.Wait()
		return err
	}
	wg.Wait()
	return nil
}

func (s *server) loop(ctx context.Context, every time.Duration, fn func()) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			fn()
		}
	}
}

// snapshot records the current aggregate and flags any scope that has grown
// far beyond its recent baseline. The flag only logs: it is the signal that
// distributed inflation is happening, not a response to it (SPEC §6, §16.3).
func (s *server) snapshot() {
	rows := s.presence.snapshot()
	now := time.Now().Unix()
	if err := s.store.writeSnapshot(now, rows); err != nil {
		slog.Error("snapshot failed", "err", err)
		return
	}
	peaks, err := s.store.weekPeaks(now - 7*24*3600)
	if err != nil {
		slog.Error("week peaks failed", "err", err)
		return
	}
	for _, r := range rows {
		limit := max(s.cfg.anomalyFloor, s.cfg.anomalyFactor*peaks[r.Scope+"/"+r.Code])
		if r.Online > limit {
			slog.Warn("presence anomaly",
				"scope", r.Scope, "code", r.Code, "online", r.Online, "limit", limit)
		}
	}
}

type heartbeatRequest struct {
	Country     string `json:"country"`
	Subdivision string `json:"subdivision"`
}

type heartbeatResponse struct {
	counts
	// SubdivisionCode echoes the subdivision actually counted, which is empty
	// when the client sent one this server does not recognise. Without it a
	// client carrying a stale catalog would render its own area as a
	// permanent zero instead of falling back to country scope (SPEC §9.4).
	SubdivisionCode string `json:"subdivision_code"`
	Next            int    `json:"next"`
}

func (s *server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	addr, ok := clientAddr(r.RemoteAddr, r.Header.Get("X-Forwarded-For"), s.cfg.trusted)
	if !ok {
		http.Error(w, "unresolvable peer", http.StatusBadRequest)
		return
	}

	var req heartbeatRequest
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, 4096), &req); err != nil {
		http.Error(w, "malformed body", http.StatusBadRequest)
		return
	}

	country := normalizeCode(req.Country, 2)
	if len(country) != 2 || !s.catalog.hasCountry(country) {
		http.Error(w, "unknown country", http.StatusBadRequest)
		return
	}
	sub := s.catalog.resolveSubdivision(country, normalizeCode(req.Subdivision, 8))

	c, err := s.presence.beat(s.presence.key(addr), country, sub, time.Now())
	switch {
	case errors.Is(err, errRateLimited):
		w.Header().Set("Retry-After", strconv.Itoa(int(s.cfg.minBeat.Seconds())))
		http.Error(w, "too many heartbeats", http.StatusTooManyRequests)
		return
	case errors.Is(err, errAtCapacity):
		http.Error(w, "at capacity", http.StatusServiceUnavailable)
		return
	case err != nil:
		http.Error(w, "heartbeat failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, heartbeatResponse{
		World:           c.World,
		Country:         c.Country,
		Subdivision:     c.Subdivision,
		SubdivisionCode: sub,
		Next:            s.nextBeat,
	})
}

type historyResponse struct {
	Scope      string     `json:"scope"`
	Code       string     `json:"code"`
	Resolution string     `json:"resolution"`
	Points     [][2]int64 `json:"points"`
	TodayPeak  int        `json:"today_peak"`
	TodayLow   int        `json:"today_low"`
	WeekPeak   int        `json:"week_peak"`
}

func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	code := normalizeCode(r.URL.Query().Get("code"), 8)
	if !s.validScope(scope, code) {
		http.Error(w, "unknown scope", http.StatusBadRequest)
		return
	}

	window := 24 * time.Hour
	if r.URL.Query().Get("range") == "7d" {
		window = 7 * 24 * time.Hour
	}
	now := time.Now()
	weekStart := now.Add(-7 * 24 * time.Hour).Unix()

	points, err := s.store.points(scope, code, now.Add(-window).Unix())
	if err != nil {
		slog.Error("history points failed", "err", err)
		http.Error(w, "history unavailable", http.StatusInternalServerError)
		return
	}
	dayPeak, dayLow, weekPeak, err := s.store.stats(scope, code, now.Add(-24*time.Hour).Unix(), weekStart)
	if err != nil {
		slog.Error("history stats failed", "err", err)
		http.Error(w, "history unavailable", http.StatusInternalServerError)
		return
	}

	body, err := json.Marshal(historyResponse{
		Scope:      scope,
		Code:       code,
		Resolution: "5m",
		Points:     points,
		TodayPeak:  dayPeak,
		TodayLow:   dayLow,
		WeekPeak:   weekPeak,
	})
	if err != nil {
		http.Error(w, "history unavailable", http.StatusInternalServerError)
		return
	}

	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func (s *server) validScope(scope, code string) bool {
	switch scope {
	case "world":
		return code == "WORLD"
	case "country":
		return s.catalog.hasCountry(code)
	case "subdivision":
		return s.catalog.hasSubdivision(code)
	}
	return false
}

// clientAddr resolves the address a presence key is derived from.
//
// The chain in production is Cloudflare -> nginx -> here. nginx is configured
// with Cloudflare's ranges and real_ip_header CF-Connecting-IP, so the entry
// it appends to X-Forwarded-For is the true client. Only that rightmost entry
// is read; anything to its left may have been supplied by the client itself
// and is ignored. A peer outside the trusted set is taken at face value, which
// is what a direct hit on the origin is (SPEC §5.3).
func clientAddr(remoteAddr, forwarded string, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		host, _, splitErr := net.SplitHostPort(remoteAddr)
		if splitErr != nil {
			host = remoteAddr
		}
		addr, parseErr := netip.ParseAddr(host)
		if parseErr != nil {
			return netip.Addr{}, false
		}
		return addr.Unmap(), true
	}

	addr := peer.Addr().Unmap()
	if forwarded == "" || !isTrusted(addr, trusted) {
		return addr, true
	}
	last := forwarded
	if _, after, found := strings.CutLast(forwarded, ","); found {
		last = after
	}
	fwd, err := netip.ParseAddr(strings.TrimSpace(last))
	if err != nil {
		return addr, true
	}
	return fwd.Unmap(), true
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, pfx := range trusted {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.MarshalWrite(w, v); err != nil {
		slog.Error("write json", "err", err)
	}
}
