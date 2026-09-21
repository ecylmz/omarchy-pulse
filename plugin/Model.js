.pragma library

// Pure helpers for Omarchy Pulse. Everything here is side-effect free so it
// can be exercised by tests/model.test.js without a running shell.

var BAR_SCOPES = ["world", "country", "subdivision"]
var BAR_STYLES = ["icon-count", "pulse-prefix"]
var BLOCKS = ["▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"]

function defaults() {
  return {
    enabled: false,
    paused: false,
    country: "",
    subdivision: "",
    bar_scope: "subdivision",
    bar_style: "icon-count"
  }
}

function oneOf(value, allowed, fallback) {
  return allowed.indexOf(String(value)) >= 0 ? String(value) : fallback
}

function normalizeCode(value, maxLen) {
  var s = String(value === undefined || value === null ? "" : value).trim().toUpperCase()
  if (s.length > maxLen || !/^[A-Z0-9-]*$/.test(s)) return ""
  return s
}

// parseSettings never throws and never returns a half-valid object: a corrupt
// or hand-edited settings file falls back to "not opted in" rather than to
// sharing something the user did not choose.
function parseSettings(text) {
  var out = defaults()
  var raw = null
  try {
    raw = JSON.parse(String(text || ""))
  } catch (e) {
    return out
  }
  if (!raw || typeof raw !== "object") return out

  out.enabled = raw.enabled === true
  out.paused = raw.paused === true
  out.country = normalizeCode(raw.country, 2)
  if (out.country.length !== 2) out.country = ""
  out.subdivision = normalizeCode(raw.subdivision, 8)
  if (out.country === "" || out.subdivision.indexOf(out.country + "-") !== 0)
    out.subdivision = ""
  out.bar_scope = oneOf(raw.bar_scope, BAR_SCOPES, "subdivision")
  out.bar_style = oneOf(raw.bar_style, BAR_STYLES, "icon-count")
  return out
}

function serializeSettings(s) {
  var clean = parseSettings(JSON.stringify(s || {}))
  return JSON.stringify(clean, null, 2) + "\n"
}

function formatCount(n) {
  var v = Math.max(0, Math.floor(Number(n) || 0))
  return String(v).replace(/\B(?=(\d{3})+(?!\d))/g, ",")
}

// Small counts are shown exactly and spelled out, because a "2" that someone
// might be curious about is the whole point (SPEC §10).
function presenceLabel(n) {
  var v = Math.max(0, Math.floor(Number(n) || 0))
  if (v === 0) return "Nobody here right now."
  if (v === 1) return "Just you here."
  return formatCount(v) + " of you here."
}

function barText(style, count, state) {
  if (state === "setup") return "◎"
  if (state !== "live") return "◌"
  return oneOf(style, BAR_STYLES, "icon-count") === "pulse-prefix"
    ? "P: " + formatCount(count)
    : "◉ " + formatCount(count)
}

// The stored bar_scope is a preference, not a fact: a country-only user has
// no subdivision, so a stored "subdivision" would leave the bar on a scope
// that can never have a count. Resolve it at display time rather than
// rewriting the preference, so picking an area later still honours it.
function effectiveScope(settings) {
  var s = settings || defaults()
  var scope = oneOf(s.bar_scope, BAR_SCOPES, "subdivision")
  if (scope === "subdivision" && !s.subdivision) scope = "country"
  if (scope === "country" && !s.country) scope = "world"
  return scope
}

function scopeCount(counts, scope) {
  if (!counts) return 0
  switch (oneOf(scope, BAR_SCOPES, "subdivision")) {
  case "world": return Math.max(0, Math.floor(Number(counts.world) || 0))
  case "country": return Math.max(0, Math.floor(Number(counts.country) || 0))
  default: return Math.max(0, Math.floor(Number(counts.subdivision) || 0))
  }
}

// sparkline buckets a [[unixSeconds, online], ...] series into fixed columns,
// taking the peak of each bucket. Missing timestamps are genuinely absent
// rather than zero-filled by the server, so an empty bucket reads as zero.
function sparkline(points, buckets, windowSeconds, nowSeconds) {
  var n = Math.max(1, Math.floor(buckets))
  var peaks = []
  var i
  for (i = 0; i < n; i++) peaks.push(0)
  if (!points || points.length === 0) return peaks.map(function () { return BLOCKS[0] }).join("")

  var start = nowSeconds - windowSeconds
  var high = 0
  for (i = 0; i < points.length; i++) {
    var ts = Number(points[i][0])
    var value = Math.max(0, Number(points[i][1]) || 0)
    if (!isFinite(ts) || ts < start) continue
    var slot = Math.floor((ts - start) / windowSeconds * n)
    if (slot < 0) slot = 0
    if (slot > n - 1) slot = n - 1
    if (value > peaks[slot]) peaks[slot] = value
    if (value > high) high = value
  }
  if (high === 0) return peaks.map(function () { return BLOCKS[0] }).join("")

  return peaks.map(function (v) {
    if (v <= 0) return BLOCKS[0]
    var level = Math.ceil(v / high * (BLOCKS.length - 1))
    return BLOCKS[Math.max(0, Math.min(BLOCKS.length - 1, level))]
  }).join("")
}

function countryOptions(catalog) {
  if (!catalog || !catalog.countries) return []
  return catalog.countries.map(function (c) {
    return { label: c.name, value: c.code }
  })
}

function findCountry(catalog, code) {
  if (!catalog || !catalog.countries) return null
  for (var i = 0; i < catalog.countries.length; i++)
    if (catalog.countries[i].code === code) return catalog.countries[i]
  return null
}

// The empty option is offered first and worded plainly, because country-only
// sharing must never read as the lesser choice (SPEC §7).
function subdivisionOptions(catalog, countryCode) {
  var country = findCountry(catalog, countryCode)
  var options = [{ label: "Country only", value: "" }]
  if (!country || !country.subdivisions) return options
  return options.concat(country.subdivisions.map(function (s) {
    return { label: s.name, value: s.code }
  }))
}

function countryName(catalog, code) {
  var country = findCountry(catalog, code)
  return country ? country.name : code
}

function subdivisionName(catalog, countryCode, code) {
  var country = findCountry(catalog, countryCode)
  if (!country || !country.subdivisions) return code
  for (var i = 0; i < country.subdivisions.length; i++)
    if (country.subdivisions[i].code === code) return country.subdivisions[i].name
  return code
}

function locationLabel(catalog, countryCode, subdivisionCode) {
  if (!countryCode) return "Not set"
  var name = countryName(catalog, countryCode)
  if (!subdivisionCode) return name
  return name + " → " + subdivisionName(catalog, countryCode, subdivisionCode)
}

// curl is asked to print the status code on a line of its own instead of
// being run with -f, so that a 429 — a healthy "you are already counted" —
// is not indistinguishable from an unreachable server.
function parseHeartbeat(raw) {
  var text = String(raw === undefined || raw === null ? "" : raw)
  var cut = text.lastIndexOf("\n")
  var status = parseInt(cut < 0 ? text : text.slice(cut + 1), 10)
  if (!isFinite(status) || status === 0) return { status: 0 }
  if (status !== 200) return { status: status }

  var parsed = null
  try {
    parsed = JSON.parse(text.slice(0, cut).trim())
  } catch (e) {
    return { status: 0 }
  }
  if (!parsed || typeof parsed.world !== "number") return { status: 0 }
  return {
    status: 200,
    counts: {
      world: parsed.world,
      country: parsed.country,
      subdivision: parsed.subdivision
    },
    subdivisionCode: String(parsed.subdivision_code || ""),
    next: parsed.next
  }
}

// Only these fields change what the server counts. bar_scope and bar_style are
// display preferences, and beating for them spent the server's rate limit and
// then reported the resulting 429 as a lost connection.
function presenceChanged(before, after) {
  var a = before || defaults()
  var b = after || defaults()
  return a.country !== b.country || a.subdivision !== b.subdivision
    || a.enabled !== b.enabled || a.paused !== b.paused
}

function heartbeatBody(country, subdivision) {
  var payload = { country: country }
  if (subdivision) payload.subdivision = subdivision
  return JSON.stringify(payload)
}

// The server dictates the cadence so it can widen the interval under load
// without a client release; failures back off so a server that is down all
// day costs a handful of retries rather than one a minute (SPEC §14.1).
function nextInterval(serverNext, failures) {
  var base = Math.floor(Number(serverNext) || 60)
  if (!isFinite(base)) base = 60
  base = Math.max(30, Math.min(600, base))
  var misses = Math.max(0, Math.min(4, Math.floor(Number(failures) || 0)))
  if (misses === 0) return base
  return Math.min(600, base * Math.pow(2, misses))
}
