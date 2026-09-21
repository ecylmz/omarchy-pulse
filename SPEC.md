# Omarchy Pulse — Specification v2

> Revision note: v2 replaces the client-generated session model with a
> server-derived, IP-keyed ephemeral presence key. The reason is in §4.
> It also drops Redis, drops the versioned location-catalog distribution
> system, and corrects the client sections to match how Omarchy shell
> plugins actually work.

---

# 1. Overview

**Omarchy Pulse** is an opt-in ambient presence system for the Omarchy
ecosystem.

It is not a social network, chat application, user directory, or activity
tracker. Pulse exists to answer one question:

> **How many of us are here right now?**

Omarchy is a niche ecosystem. Seeing that other people are actively using it
at the same time — late at night, or in the same city — creates a small but
real sense of community. Pulse provides that feeling without requiring any
direct interaction between users.

> **Pulse does not connect you to people. It reminds you that they are there.**

Social interaction, if it happens, happens outside Pulse via the visible
**#OmarchyPulse** hashtag.

---

# 2. Product Principles

## 2.1 Ambient presence, not social networking

Pulse does **not** provide chat, direct messages, usernames, profiles,
follower relationships, user lists, per-user history, public identities, or
per-user location maps. Pulse shows aggregated counts only.

## 2.2 Collect presence, not identity

Pulse collects the minimum needed to produce an aggregate count. No account,
no email, no username, no persistent device identity, no GPS, no
fingerprinting, no IP-based geolocation.

The one thing Pulse *does* use the IP address for — and only in RAM, only as
an anti-abuse bucket key — is specified precisely in §5. Being honest about
that is part of the principle, not an exception to it.

## 2.3 Explicit opt-in

Presence sharing is off until the user enables it. Installing the plugin must
not start sending anything.

```text
Enable Pulse presence?

Pulse periodically tells the server:
• the country and area you picked yourself
• nothing else

The server derives a temporary 3-minute counting slot from your
connection, so one machine cannot be counted a thousand times.
Your IP is never stored, never logged, and never used to guess
where you are.

Small areas show small numbers. If your area shows 1, that number
is you — anyone who learns that will also see when you are online.
Pick "country only" if you would rather not.

[ Enable Pulse ]     [ Not now ]
```

---

# 3. Terminology

| Term | Meaning |
|---|---|
| **Pulse** | The system and the Omarchy plugin. |
| **Presence** | A temporary indication that an Omarchy system is active. |
| **Scope** | A geographic aggregation level: `world`, `country`, `subdivision`. |
| **Presence key** | A server-derived, in-memory-only identifier for one presence slot (§5). |
| **Heartbeat** | A periodic request indicating the client is still active. |
| **Catalog** | The static ISO 3166 country/subdivision dataset. |

---

# 4. Threat Model

This section drives the protocol design. It is placed before the architecture
because it changes it.

## 4.1 The core problem

The entire product is a single number. If that number is forgeable, the
product is worthless.

With anonymous client-generated session IDs and no account, a one-line shell
loop produces unlimited presence:

```bash
while :; do
  curl -X POST $API/v1/heartbeat -d "{\"session\":\"$RANDOM$RANDOM\",...}"
done
```

The damage is *worse* because Pulse deliberately shows small numbers (§8).
`Samsun 2` is the product. `Samsun 40,000` is the end of it.

## 4.2 Threats

| # | Threat | Severity |
|---|---|---|
| T1 | Single-host inflation (loop from one machine) | Critical |
| T2 | Targeted inflation of one small scope | Critical |
| T3 | Distributed inflation (botnet / many VPS IPs) | High |
| T4 | Scope pollution (invented country/subdivision codes) | Medium |
| T5 | Header spoofing behind a misconfigured proxy | Critical |
| T6 | Resource exhaustion (unbounded key map, request flood) | Medium — see §4.4 |
| T7 | Deflation (removing others' presence) | Low — not possible; presences are independent |
| T8 | Individual disclosure via a scope count of 1 | Medium — see §7 |
| T9 | Claiming many locations, or switching between them rapidly | Low — structurally bounded, see §4.3 |

## 4.3 One address cannot be in two places

The obvious attack on a location-based counter is to script the endpoint and
claim every city at once, or to flip between them fast enough to be counted in
several. Neither works, and not because of a rule: because of the shape of the
data.

Live presence is `map[presenceKey]entry`, and an `entry` holds exactly **one**
country and **one** subdivision. The key is the identity (§5), so one address
owns exactly one entry. A heartbeat that arrives with a different location does
not add anything — it releases the counters for the old location and claims the
new one, as a pair, under one lock. The entry is overwritten, never duplicated.

So an address can *move*, arbitrarily and dishonestly, but at any instant it
contributes exactly `1`, to exactly one subdivision, one country, and the world.
Being in two cities at once would require two entries, which would require two
addresses — which is T3, and costs real money.

Verified two ways. Structurally, by two tests: a tour through eight cities
asserting that after every move exactly one country and at most one subdivision
hold a count, each at exactly one; and 800 concurrent beats from 16 addresses
under `-race`, asserting the incremental counters still match a full recount,
since a torn update here would be an inflation bug. Empirically, against the
deployed server:

```text
Samsun                      → world 1, TR 1, TR-55 1
Istanbul, within 5s         → 429, nothing moved
Istanbul, after 7s          → world 1, TR 1, TR-34 1
Tokyo, after 7s             → world 1, JP 1, JP-13 1
```

World stays at `1` throughout. That is the whole answer.

### What remains, stated plainly

**Location was never a truth claim.** It is selected by hand and Pulse
deliberately refuses to check it against the network (§2.2, §12). A client
asserting Tokyo from Samsun is not an attack; it is the documented model. The
guarantee is *one presence per address*, not *an honest location*.

Given that, rapid switching buys an attacker almost nothing a patient one could
not get by picking a city and staying there. The one artifact it does leave is
a trail through history: a client flipping every few seconds appears in
whichever scope it occupied when each five-minute snapshot fired, so a day of
it can leave `peak today 1` in a few hundred cities that were in fact empty.

That is capped at `1` per address and is indistinguishable from one real person
who travels absurdly, so no control is added for it. A location-change rate
limit was considered and rejected: it would cost code and punish the rare
honest move, while an attacker who actually wants many cities at once still
needs many addresses either way.

## 4.4 Flooding the API

Nothing stops someone sending 100 requests a second at a published endpoint,
so the question is only what that costs the service. Two things had to be
built, because the first draft of this document promised one of them and the
deployment did not have it.

**Rate limit at the edge.** nginx carries `limit_req` at 5r/s per address with
a burst of 20, keyed on `$binary_remote_addr` — which the real-ip
configuration (§5.3) makes the true client, so the limit holds whether a
request arrives through the CDN or straight at the origin. A flood is answered
with `429` by nginx and never reaches Go. 5r/s is absurdly generous for a
client that beats once a minute, and comfortable even for a large NAT: fifty
Omarchy users behind one address produce under 1r/s.

This is only safe to set that low because a `429` is handled correctly (§14.1):
a shared address that does clip the limit retries and keeps its count, rather
than reporting itself offline.

**Cache the expensive endpoint.** `GET /v1/history` was by a wide margin the
cheapest thing to flood — a range scan plus an aggregate, against a database
deliberately held on one connection (§15.2), so concurrent reads serialise.
Measured on the deployment before the fix: 115ms for one request, 321ms average
at 20 concurrent. Since the data only changes when a snapshot is written every
five minutes, each response is now held in memory for 30 seconds. Repeated
reads never reach SQLite, and the `ETag` stays stable across the window so
clients that already have the data are answered `304` with no body.

The heartbeat needs no such cache: it is a map lookup under a mutex, and the
edge limit bounds how often any address can ask.

### What remains

* **A genuinely distributed flood** — thousands of addresses, each politely
  under 5r/s — still arrives. The history cache absorbs the expensive half;
  heartbeats contend on the single presence mutex, whose upgrade path is
  recorded at its declaration. Cloudflare absorbs volumetric attacks, but no
  application-layer rate limiting rule is configured there.
* **The origin is directly reachable.** Its address is not secret and no
  firewall restricts it, so Cloudflare's absorption can be bypassed by anyone
  who looks it up. The per-address limit still applies, because it lives in
  nginx rather than at the CDN. Restricting the origin to Cloudflare's ranges
  was considered and rejected: the host serves several other applications on
  the same ports that are deliberately reached directly, and a stale range list
  would take the whole site down rather than degrade it.
* **Memory is bounded** rather than defended: `MAX_KEYS` caps live presences at
  200,000 with a `503` beyond it, request bodies are capped at 4 KiB, and the
  history cache is capped at 4,096 entries against a key space the catalog
  bounds at roughly 3,800.

## 4.5 Explicitly rejected defenses

* **API key or secret shipped in the plugin.** The plugin is open source and
  reads as plain text on every user's disk. Extracted in minutes. Provides no
  security and creates the illusion of some.
* **Client attestation / proof-of-install.** Same problem, more code.
* **Accounts or captchas.** Destroys the product.
* **Proof-of-work on the heartbeat.** Costs every honest laptop battery
  forever to inconvenience an attacker for one afternoon.

## 4.6 Residual risk, stated honestly

Abuse cannot be driven to zero in an anonymous, account-free, open-source
system. It can be made to **cost real resources**, be **bounded in blast
radius**, and be **visible when it happens**. §5, §6 and §16 do those three
things. An attacker with a genuine botnet can still inflate a scope; §16.4
describes the prepared response and why the data needed for it already exists.

---

# 5. Presence Identity

**There is no client-supplied session identifier.** The client never invents,
stores, or transmits an ID.

The server derives the presence key itself:

```text
key = HMAC-SHA256(boot_secret, ip_prefix)[:16]
```

* `boot_secret` — 32 random bytes generated at process start. Never written
  to disk, never logged, never exposed. A restart invalidates every key; the
  map refills within one heartbeat interval.
* `ip_prefix` — the full address for IPv4, the `/56` prefix for IPv6
  (configurable). The `/56` follows typical residential delegation and keeps
  a single subscriber from counting as thousands of addresses.

The key exists only as a map entry with a 180-second TTL. Nothing else is
derived from it and nothing is persisted from it.

## 5.1 Why this replaces the session model

| | Client session ID | Server-derived key |
|---|---|---|
| Cost of one extra presence | one `$RANDOM` | one distinct IP prefix |
| Identity stored client-side | yes (or regenerated) | none |
| Fields in the heartbeat | 4 | 2 |
| Needs "session rotation" feature | yes | no — restart rotates everything |

It is simultaneously smaller, more private (nothing identifying is stored on
either side), and the only one of the two that survives a shell loop.

## 5.2 Accepted inaccuracies

* Two Omarchy users on the same home LAN count as **one**.
* Users behind CGNAT, a university NAT, Tor, or a shared VPN exit count as
  **one** for that exit.
* An IPv6 privacy-extension address rotation within the same `/56` does not
  double-count; a `/56` change does, for up to 180 seconds.

Undercounting at NAT is the correct trade. Pulse promises an honest sense of
"someone else is here", not a census. Unbounded inflation breaks that promise;
counting two roommates as one does not.

## 5.3 Trust boundary — mandatory

If Pulse runs behind a reverse proxy or CDN, the server **must** be configured
with an explicit list of trusted proxy addresses and must take the client IP
only from that proxy's header.

An unrestricted `X-Forwarded-For` parse means any client can assert any IP and
mint unlimited presence keys. **This is the single most damaging possible
misconfiguration.** The server must refuse to start if it is configured to
trust a forwarded header without a trusted-proxy list.

---

# 6. Anti-Abuse Controls

| Control | Behavior |
|---|---|
| **Presence key** (§5) | One IP prefix contributes at most one presence to at most one scope. Addresses T1, T2, partly T3. |
| **Heartbeat rate limit** | Max one accepted heartbeat per key per 5s. Excess → `429` with `Retry-After`. Addresses T6 only: because a key is derived from the address, beating more often cannot produce more presences, so the limit is deliberately loose enough not to punish a user changing their location. |
| **Country validation** | `country` must exist in the embedded catalog, else `400`. Addresses T4. |
| **Subdivision validation** | Unknown but well-formed subdivision → counted at world and country scope only, never `400`. Keeps stale clients working (§9.4). Addresses T4. |
| **Scope closure** | Counts exist only for catalog entries. No client input creates a scope. |
| **Key map bound** | Hard cap on live keys; beyond it, new keys are refused with `503`. Addresses T6. |
| **Edge rate limit** | nginx `limit_req` at 5r/s per address, burst 20, answered with `429`. Stops a flood before it reaches the application at all. Addresses T6. |
| **History cache** | Each history response is held in memory for 30s, so repeated reads never reach SQLite. Addresses T6. |
| **Anomaly log** | A scope exceeding `max(20, 5 × its 7-day peak)` logs a warning. No behavior change — this is the signal that T3 is happening. |

The anomaly log is the only piece here whose purpose is future: it costs a few
lines and is the difference between discovering an attack and being told about
it on social media.

---

# 7. Individual Disclosure

Pulse deliberately does **not** hide counts below a privacy threshold. Small
exact numbers are the product (§10).

The consequence must be stated rather than discovered:

> A subdivision showing `1` is one person's machine. Combined with
> #OmarchyPulse — where someone may well post "the one in Samsun is me" —
> that count becomes a public online/offline signal for an identifiable
> individual.

Mitigations, none of which hide the number:

1. The opt-in copy says this in plain language (§2.3).
2. Country-only sharing is offered with equal weight in the location picker,
   never as a lesser option.
3. Pause is one click in the panel and takes effect in ≤180s (§13.3).

---

# 8. Geographic Model

```text
World
└── Country            (ISO 3166-1 alpha-2)
    └── Subdivision    (ISO 3166-2)
```

One flexible level below country, whatever it is called locally — state,
province, prefecture, il, Bundesland, region. No deeper level. Pulse never
asks for a street, neighborhood, GPS coordinate, or exact location.

The user may stop at country.

## 8.1 Catalog

The catalog is **one file**, `locations.json`, roughly 100 KB, generated from
ISO 3166-1 and ISO 3166-2 by `make locations` and committed to the repo.

```json
{
  "generated": "2026-09-21",
  "countries": [
    { "code": "TR", "name": "Türkiye", "subdivisions": [
        { "code": "TR-55", "name": "Samsun" },
        { "code": "TR-52", "name": "Ordu" } ] }
  ]
}
```

The server embeds it with `go:embed`. The plugin reads its own vendored copy.
Both live in the same repo, so they are generated and released together.

**Not built:** manifest files, per-country revision numbers, lazy per-country
downloads, ETag negotiation on catalog fetches, a CDN, a CI regeneration
pipeline, a catalog version independent of the plugin version. Country
subdivisions change on a scale of years. A plugin release covers it. Drift
between a stale plugin and a newer server is handled gracefully by §6's
subdivision rule, so the failure mode is "your city stops being listed
separately", not an error.

---

# 9. Client — Omarchy Shell Plugin

Pulse is a Quickshell **bar-widget plugin**, matching the structure of the
existing Omarchy panels (`weather`, `tailscale`) and third-party plugins.

```text
manifest.json        schemaVersion 1, kinds: ["bar-widget"]
BarWidget.qml        entry point; WidgetButton + Panel Loader
Panel.qml            popout, anchored to the bar button
Service.qml          heartbeat timer, curl Process, state
Model.js             pure functions (parsing, formatting, validation)
locations.json       vendored catalog
```

```json
{
  "schemaVersion": 1,
  "id": "ecylmz.omarchy-pulse",
  "name": "Omarchy Pulse",
  "kinds": ["bar-widget"],
  "entryPoints": { "barWidget": "BarWidget.qml" },
  "barWidget": {
    "displayName": "Omarchy Pulse",
    "category": "Community",
    "allowMultiple": false,
    "defaultSection": "right"
  }
}
```

## 9.1 Bar position

**Removed from the spec.** Omarchy already owns this: `defaultSection` in the
manifest sets the default, and the user moves it with
`omarchy bar move ecylmz.omarchy-pulse --section center` or by editing
`~/.config/omarchy/shell.json`, which hot-reloads. Pulse must not ask.

## 9.2 Settings persistence

Following the `weather` panel precedent, via `FileView`:

```text
~/.local/state/omarchy/settings/pulse.json
```

```json
{
  "enabled": true,
  "country": "TR",
  "subdivision": "TR-55",
  "bar_scope": "subdivision",
  "bar_style": "icon-count"
}
```

`bar_scope` ∈ `world | country | subdivision`.
`bar_style` ∈ `icon-count` (`◉ 16`) | `pulse-prefix` (`P: 16`).

No session ID is stored, because none exists (§5).

## 9.3 Networking

HTTP through `Quickshell.Io.Process` running `curl`, as the built-in panels
do:

```text
curl -fsS --max-time 5 -X POST -H 'content-type: application/json' \
     -d '{"country":"TR","subdivision":"TR-55"}' $API/v1/heartbeat
```

Exactly one in-flight request at a time per request kind. The heartbeat timer
runs whether or not the panel is open; the `Panel.qml` Loader stays
`active: true`, as in the existing plugins.

## 9.4 Version drift

The client must treat an unknown response field as ignorable and a missing
optional field as absent. A newer server must never break an older plugin.

---

# 10. Bar Indicator

| State | Display | Tooltip |
|---|---|---|
| Enabled, no location chosen | `◎` | not set up yet |
| Live | `◉ 16` or `P: 16`, per `bar_style` | the presence sentence for the shown scope |
| No count yet | `◌` | connecting… |
| Paused | `◌` | paused |
| Server unreachable | `◌` | can't reach the server |

`◌` means "no number to show" rather than specifically "offline"; the tooltip
carries the reason. A count is only ever printed when the server has actually
returned one.

The count shown is the scope in `bar_scope`, which is independent of the
location being shared. Sharing as `Türkiye → Samsun` while the bar shows
`World` is valid and expected.

`bar_scope` is a stored *preference*, not a fact about what can be shown. A
user sharing at country granularity has no subdivision, so a stored
`subdivision` scope resolves down — subdivision → country → world — to the most
specific scope actually being shared. The preference is never rewritten, so
choosing an area later brings it back. Resolving this at display time rather
than on save is what keeps the bar off a scope that can only ever read `0`.

The bar never prints verbose geography, never emits error text, and never
blocks bar rendering.

---

# 11. Pulse Panel

Calm and atmospheric, not a monitoring dashboard. It must not resemble
analytics software, a social client, or a messaging app.

```text
PULSE                         ● live

Samsun                            16
Türkiye                          143
World                          2,841

LAST 24 HOURS

▁▁▂▃▄▆▇▆▅▃▂▂▃▄▅▃

Peak today                        24
Quietest                           3

Sharing as    Türkiye → Samsun     ✎
Status        ● Active             ⏸

#OmarchyPulse
```

Contextual microcopy for small counts:

```text
Just you here.
2 of you here.
16 of you here.
```

---

# 12. #OmarchyPulse

The hashtag is visible in the panel. Pulse implements no chat. Users who want
to find each other may voluntarily use the hashtag on external platforms.
This is the entire social layer, and it lives outside the product.

---

# 13. Presence States

## 13.1 v1

`Active` only. The heartbeat carries no status field, because nothing consumes
one.

## 13.2 Later

`Focus` and `Away` may be added as a `status` field aggregated per scope. The
history schema already has the columns (§15.1).

## 13.3 Pause

"Invisible" is not a transmitted state. Pause simply stops the heartbeat; the
server-side TTL removes the presence within 180 seconds with no server-side
work, no extra endpoint, and no way for the server to distinguish a paused
user from a closed laptop.

---

# 14. API

```text
POST /v1/heartbeat
GET  /v1/history?scope=subdivision&code=TR-55&range=24h
```

That is the whole API.

## 14.1 Heartbeat

```json
{ "country": "TR", "subdivision": "TR-55" }
```

`subdivision` is optional. No session field. No status field. No IP field.

Response:

```json
{
  "world": 2841,
  "country": 143,
  "subdivision": 16,
  "subdivision_code": "TR-55",
  "next": 60
}
```

`subdivision_code` echoes the subdivision actually counted, and is empty when
the server did not recognise the one sent. Without it, a client carrying an
older catalog would render its own area as a permanent `0` rather than falling
back to country scope.

The heartbeat **returns the counts**. The bar needs no separate poll: one
request per 60 seconds does both jobs, halving traffic against the v1 design
and keeping the bar exactly as fresh as the presence it reports.

`next` is the server-dictated seconds until the next heartbeat. It lets the
server widen the interval under load or attack without a client release. The
client clamps it to `[30, 600]`.

Errors: `400` invalid country · `429` rate limited, honor `Retry-After` ·
`503` at capacity.

A `429` is not a failure. It means the beat arrived inside the minimum
interval and was **not evaluated**, so nothing the server would have reported
has changed and the server is plainly reachable. The client therefore reads the
status code rather than relying on `curl -f`, which cannot tell a rate limit
from an unreachable host, and it retries shortly so the beat actually lands.

Two things follow from "not evaluated", and both were bugs before they were
rules:

* A `429` carries no counts, so a client that has never had a successful beat
  has nothing to show. It must not print a confident `0`; it reports
  `connecting` until a beat lands.
* Counts belong to the location the server last accepted. When the user
  changes location, the old counts do not describe the new one, so they are
  discarded rather than redisplayed under the new label.

Retries are bounded. A client that is permanently rate limited — two machines
behind one NAT share a presence key, so it can happen — falls back to the
ordinary failure backoff instead of polling forever, and the retry budget is
cleared only by a beat that lands.

`MIN_BEAT_SECONDS` must not exceed `PRESENCE_TTL_SECONDS`, or a client could be
rate limited out of its own presence. The server refuses to start otherwise.

## 14.2 History

Fetched only when the panel opens, cached ~5 minutes client-side, and served
with `Cache-Control: max-age=300` plus `ETag`.

```json
{
  "scope": "subdivision",
  "code": "TR-55",
  "resolution": "5m",
  "points": [[1790010000, 2], [1790010300, 3], [1790010600, 3]],
  "today_peak": 24,
  "week_peak": 31,
  "today_low": 3
}
```

Peaks ride along in the same response rather than in a separate endpoint.
`today_peak`, `today_low` and `week_peak` are rolling 24-hour and 7-day
windows, not calendar days: the server holds no timezone for anyone, because
it knows nothing about anyone.

---

# 15. Server

```text
Go binary  ──  in-memory presence map  ──  SQLite (aggregate history)
```

Single static binary. No Redis.

## 15.1 Live presence

```go
map[key]entry{ country, subdivision string; expires time.Time }
```

guarded by a `sync.RWMutex`, swept every 30 seconds.

**Redis was removed.** Its stated purpose was surviving deploys — but the TTL
is 180s and the heartbeat interval 60s, so a restart costs at most 60 seconds
of accuracy and self-heals with no code. That is not worth a second stateful
service, a persistent volume, an AOF tuning decision, and a network hop.
A map handles orders of magnitude more presences than this ecosystem will
produce.

> `ponytail: single global mutex over the presence map; shard by key prefix
> if contention ever shows up in a profile.`

## 15.2 History

```sql
CREATE TABLE presence_history (
    scope  TEXT    NOT NULL,   -- world | country | subdivision
    code   TEXT    NOT NULL,   -- WORLD | TR | TR-55
    ts     INTEGER NOT NULL,
    online INTEGER NOT NULL,
    active INTEGER NOT NULL DEFAULT 0,
    focus  INTEGER NOT NULL DEFAULT 0,
    away   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scope, code, ts)
) WITHOUT ROWID;
```

The key leads with `(scope, code)` so that reading one scope's series is a
single range scan of the primary key, which is the only read shape the product
has. No secondary index is needed.

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;
PRAGMA busy_timeout = 5000;
```

A snapshot every 5 minutes writes **only scopes with `online > 0`.** Writing
every catalog scope would be ~4,200 rows × 288/day ≈ 1.2M rows/day, ~99% of
them zeros. Skipping zeros makes the table proportional to actual usage, and a
gap in the series reads as zero at query time.

No key, no session, no IP, no per-user row ever reaches SQLite.

```text
In memory: who is here right now.
SQLite:    how many people were here.
```

Retention: keep 5-minute rows as-is. Downsample only if the table ever becomes
a problem, which at this scale it will not.

---

# 16. Operations

## 16.1 Deployment

```text
pulse            single Go binary, scratch image, uid 32767
/data/pulse.db   persistent volume
reverse proxy    trusted-proxy list configured (§5.3), request logging
                 off (§16.2)
```

The deployed chain is Cloudflare → nginx → container:

* nginx carries `set_real_ip_from` for Cloudflare's published ranges plus
  `real_ip_header CF-Connecting-IP`, so `$remote_addr` becomes the true client
  and a `CF-Connecting-IP` from any other source is ignored.
* nginx then sets `X-Forwarded-For` to exactly `$remote_addr`, replacing
  whatever the client sent rather than appending to it.
* The server trusts only the proxy's own CIDR and reads the rightmost
  `X-Forwarded-For` entry, so it stays correct under either nginx convention.

Regenerate the Cloudflare ranges with `tools/gen-cloudflare-nginx.sh` when they
change. A stale list degrades a whole edge into one presence; it never grants
anyone a forged one.

If a CDN or proxy provider is used, the documentation must state plainly that
the infrastructure provider sees source IPs as part of normal network
operation. Pulse's claim is about what Pulse stores, not about what the
internet is.

## 16.2 Request logging

The privacy statement says an address is never written to a log, so the
deployment has to actually be that. It was not: the vhost's default
`access_log` was writing the client address next to `POST /v1/heartbeat` and a
timestamp on every beat, which is a presence record in all but name.

What it takes, and why each part is not obvious:

* `access_log off;` in the app's server context. `access_log` is **additive** in
  nginx, so adding an anonymised format alongside the default logs twice rather
  than replacing it, and `off` is the only thing that cancels the inherited
  line. `off` also cancels any `access_log` added after it at the same level,
  so an anonymised log cannot be kept as a consolation prize.
* `limit_req_log_level info;`. `error_log` has no `off` and is likewise
  additive, so the vhost's own line at the default `error` level cannot be
  removed — the level of the *message* has to move below the threshold
  instead. Without this a rate-limited flood still lands in the error log with
  an address attached.
* The `log_format` and `limit_req_zone` live in an http-context file named to
  sort **before** `dokku.conf`, which is what includes the app vhosts. A
  `log_format` is resolved while the vhost is parsed, so declaring it later
  fails the entire configuration with `unknown log format`. A `limit_req` zone
  resolves after parsing and is not order-sensitive, which is why the rate
  limit worked from a later-sorting file and the log format did not.

Verified by flooding the deployment — normal traffic plus sixty requests, of
which thirty-four were rate limited — and then searching every nginx and
application log for the source address. Both files stayed empty.

The cost is deliberate: a flood now leaves no trace on the host. It is
affordable because the limit already makes a flood harmless, because the CDN's
own analytics give volumetric visibility without Pulse storing anything, and
because an unconditional promise is worth more to this product than an
attacker's address. Infrastructure providers still see source addresses as part
of normal network operation, which §16.1 says plainly.

## 16.3 Scaling

Single instance. Horizontal scaling was removed from the spec: it was in v1
only because Redis made it possible, and it brought a leader-election
requirement for the SQLite writer with it. One Go process serving a niche
ecosystem's heartbeats is not the bottleneck anyone will hit. If it ever is,
§15.1's map is the only thing that needs to move.

## 16.4 Prepared response to distributed abuse

Not built in v1, deliberately. Documented so it can be added without touching
the protocol:

* **Freeze list** — a reloadable config of scopes whose published count is
  pinned to their last known good value. Roughly ten lines, no deploy needed.
* **Growth clamp** — a published count may rise by at most
  `max(2, 25%)` per snapshot interval toward its true value. Delays honest
  growth by minutes; caps an attack at a visible crawl.

Both depend only on the 7-day peak, which §15.2 already stores, and on the
anomaly log in §6, which is v1. Nothing about the API or the client changes.

---

# 17. Privacy Statement

> **Collect presence, not identity.**

Expanded, and accurate:

> Pulse has no accounts, usernames, emails, device IDs, or profiles, and it
> never asks your device where it is. Your location is the one you picked by
> hand. Your IP address is never stored, never logged, never used to guess
> your location, and never attached to historical data; it is hashed in memory
> with a secret that exists only while the server is running, purely so that
> one machine cannot be counted a thousand times. Presence expires by itself
> after three minutes. History contains aggregate counts only.

Both the plugin and the server are open source so that every claim above is
checkable rather than promised.

---

# 18. Non-Goals

Pulse is not, and will not become: a chat platform, a Discord replacement, a
user directory, a social network, a telemetry platform, an activity tracker, a
device analytics platform, or a location tracking service.

**Decision test for any proposed feature:**

> Does this help users feel that other people are present, or does it make
> users interact directly with each other?

If it primarily enables direct interaction, it does not belong in Pulse.

## 18.1 The count is never padded

Published counts are only ever the number of live presences the server
actually observed. They are never seeded, simulated, floored at a minimum,
smoothed upward, or supplemented with anything that is not a real client.

This is not a modesty rule, it is the product. A count that might be inflated
to look healthier is worth nothing, and `World 3` on launch day is the honest
answer to the only question Pulse asks. The number grows when people install
the plugin, and there is no other mechanism by which it is permitted to grow.

The clamp in §16.4 only ever holds a count *below* its true value, never above.

---

# 19. Scope

## 19.1 v1

1. Quickshell bar-widget plugin, installable from the plugin directory.
2. Explicit opt-in with the disclosure copy from §2.3.
3. Manual country selection; optional subdivision.
4. Bar indicator with selectable `bar_scope` and `bar_style`.
5. Panel: world / country / subdivision counts, 24h sparkline, today's peak
   and low, sharing summary, pause, `#OmarchyPulse`.
6. Settings at `~/.local/state/omarchy/settings/pulse.json`.
7. Graceful degradation — `◌`, silent, never blocks bar rendering.
8. Go server, in-memory presence, 180s TTL, 60s heartbeat.
9. Server-derived IP-keyed presence (§5) with the §6 control set.
10. Trusted-proxy enforcement with refuse-to-start (§5.3).
11. SQLite 5-minute snapshots, non-zero scopes only.
12. `POST /v1/heartbeat` returning counts, `GET /v1/history`.
13. Embedded ISO 3166 catalog, vendored to the plugin.
14. Anomaly logging.

## 19.2 Later

Focus/Away states · richer history ranges · Explore mode · 3D globe ·
localized labels · freeze list and growth clamp (§16.4) · additional
administrative levels, if ever justified.

## 19.3 Globe — design constraint

The globe is not in v1, but the API must not preclude it. It visualizes
aggregate scopes only:

```text
● Samsun 16
```

one marker, never sixteen dots. No individual coordinates exist anywhere in
the system, which is what makes that constraint structural rather than
a policy.

---

# 20. Identity

**Omarchy Pulse** · short: **Pulse**

> See how many of us are here.

> **Pulse doesn't connect you to people. It reminds you that they're there.**

> **Collect presence, not identity.**

**#OmarchyPulse**

```text
◉ 2
```
