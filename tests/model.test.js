// Run with: node tests/model.test.js   (from the plugin directory)
const fs = require('fs');
const vm = require('vm');
const assert = require('assert');

const M = {};
vm.createContext(M);
// `.pragma library` is a QML directive node cannot parse; the rest is plain JS.
const source = fs.readFileSync('Model.js', 'utf8').replace(/^\.pragma .*$/m, '');
vm.runInContext(source, M);

const catalog = JSON.parse(fs.readFileSync('locations.json', 'utf8'));

// Settings come off disk and may have been hand-edited. A bad file must never
// leave the plugin sharing something the user did not choose.
{
  const d = M.parseSettings('');
  assert.strictEqual(d.enabled, false);
  assert.strictEqual(d.country, '');
  assert.strictEqual(d.bar_scope, 'subdivision');

  assert.deepStrictEqual(M.parseSettings('not json'), M.defaults());
  assert.deepStrictEqual(M.parseSettings('null'), M.defaults());
  assert.deepStrictEqual(M.parseSettings('[1,2]'), M.defaults());

  const good = M.parseSettings(JSON.stringify({
    enabled: true, country: 'tr', subdivision: 'tr-55',
    bar_scope: 'world', bar_style: 'pulse-prefix'
  }));
  assert.strictEqual(good.country, 'TR');
  assert.strictEqual(good.subdivision, 'TR-55');
  assert.strictEqual(good.bar_scope, 'world');
  assert.strictEqual(good.bar_style, 'pulse-prefix');

  // A subdivision belonging to another country is dropped, not shared.
  assert.strictEqual(M.parseSettings('{"country":"TR","subdivision":"US-CA"}').subdivision, '');
  // So is anything that is not a plain code.
  assert.strictEqual(M.parseSettings('{"country":"TR","subdivision":"TR-55; rm -rf /"}').subdivision, '');
  assert.strictEqual(M.parseSettings('{"country":"TÜRKIYE"}').country, '');
  // Unknown scopes and styles fall back rather than reaching the bar.
  assert.strictEqual(M.parseSettings('{"bar_scope":"galaxy"}').bar_scope, 'subdivision');
  assert.strictEqual(M.parseSettings('{"bar_style":"blink"}').bar_style, 'icon-count');

  // A round trip through disk must be stable.
  assert.deepStrictEqual(M.parseSettings(M.serializeSettings(good)), good);
}

// Small counts are the product, so they are shown exactly (SPEC §10).
{
  assert.strictEqual(M.presenceLabel(0), 'Nobody here right now.');
  assert.strictEqual(M.presenceLabel(1), 'Just you here.');
  assert.strictEqual(M.presenceLabel(2), '2 of you here.');
  assert.strictEqual(M.presenceLabel(16), '16 of you here.');
  assert.strictEqual(M.formatCount(2841), '2,841');
  assert.strictEqual(M.formatCount(1000000), '1,000,000');
  assert.strictEqual(M.formatCount(-5), '0');
  assert.strictEqual(M.formatCount('nonsense'), '0');
}

{
  assert.strictEqual(M.barText('icon-count', 16, 'live'), '◉ 16');
  assert.strictEqual(M.barText('pulse-prefix', 16, 'live'), 'P: 16');
  assert.strictEqual(M.barText('icon-count', 16, 'setup'), '◎');
  assert.strictEqual(M.barText('icon-count', 16, 'offline'), '◌');
  assert.strictEqual(M.barText('icon-count', 16, 'paused'), '◌');
}

{
  const counts = { world: 2841, country: 143, subdivision: 16 };
  assert.strictEqual(M.scopeCount(counts, 'world'), 2841);
  assert.strictEqual(M.scopeCount(counts, 'country'), 143);
  assert.strictEqual(M.scopeCount(counts, 'subdivision'), 16);
  assert.strictEqual(M.scopeCount(null, 'world'), 0);
  assert.strictEqual(M.scopeCount(counts, 'nonsense'), 16);
}

// The sparkline reads a server series whose gaps mean "nobody was there".
{
  const now = 1790000000;
  assert.strictEqual(M.sparkline([], 8, 86400, now).length, 8);
  assert.strictEqual(M.sparkline(null, 8, 86400, now), '▁▁▁▁▁▁▁▁');
  // All-zero history must not divide by a zero peak.
  assert.strictEqual(M.sparkline([[now - 100, 0]], 4, 86400, now), '▁▁▁▁');

  // The peak bucket is full height and an empty bucket is floor height.
  const line = M.sparkline([[now - 86000, 1], [now - 100, 10]], 4, 86400, now);
  assert.strictEqual(line.length, 4);
  assert.strictEqual(line[3], '█');
  assert.strictEqual(line[1], '▁');
  // Points older than the window are ignored rather than crammed into slot 0.
  assert.strictEqual(M.sparkline([[now - 999999, 99], [now - 10, 1]], 4, 86400, now)[0], '▁');
}

// The real catalog has to drive the pickers.
{
  const countries = M.countryOptions(catalog);
  assert.ok(countries.length > 200, `only ${countries.length} countries`);
  assert.ok(countries.some(c => c.value === 'TR' && c.label === 'Türkiye'));

  const areas = M.subdivisionOptions(catalog, 'TR');
  assert.strictEqual(areas[0].value, '', 'country-only must be offered first');
  assert.strictEqual(areas.length, 82, 'Türkiye has 81 provinces plus country-only');
  assert.ok(areas.some(a => a.value === 'TR-55' && a.label === 'Samsun'));

  // An unknown country yields country-only rather than throwing.
  // Compared as JSON: objects built inside the vm context carry that realm's
  // prototype, which deepStrictEqual treats as a difference.
  assert.strictEqual(JSON.stringify(M.subdivisionOptions(catalog, 'ZZ')),
                     JSON.stringify([{ label: 'Country only', value: '' }]));

  assert.strictEqual(M.locationLabel(catalog, 'TR', 'TR-55'), 'Türkiye → Samsun');
  assert.strictEqual(M.locationLabel(catalog, 'TR', ''), 'Türkiye');
  assert.strictEqual(M.locationLabel(catalog, '', ''), 'Not set');
}

// The bar must never sit on a scope the user is not sharing: country-only
// sharing with the default "subdivision" scope showed a permanent 0.
{
  const countryOnly = { country: 'TR', subdivision: '', bar_scope: 'subdivision' };
  assert.strictEqual(M.effectiveScope(countryOnly), 'country');
  assert.strictEqual(M.scopeCount({ world: 5, country: 1, subdivision: 0 },
                                  M.effectiveScope(countryOnly)), 1);

  // A stored preference is honoured as soon as it becomes shareable.
  assert.strictEqual(M.effectiveScope({ country: 'TR', subdivision: 'TR-55', bar_scope: 'subdivision' }), 'subdivision');
  // Nothing configured at all still resolves to something countable.
  assert.strictEqual(M.effectiveScope({ country: '', subdivision: '', bar_scope: 'country' }), 'world');
  assert.strictEqual(M.effectiveScope({ country: '', subdivision: '', bar_scope: 'subdivision' }), 'world');
  // Explicit choices are left alone.
  assert.strictEqual(M.effectiveScope({ country: 'TR', subdivision: 'TR-55', bar_scope: 'world' }), 'world');
  assert.strictEqual(M.effectiveScope(null), 'world');
}

// A 429 means "already counted", not "server unreachable". Conflating the
// two made the bar report a lost connection whenever a preference was
// toggled twice in quick succession.
{
  const ok = M.parseHeartbeat('{"world":3,"country":2,"subdivision":1,"subdivision_code":"TR-55","next":60}\n200');
  assert.strictEqual(ok.status, 200);
  assert.strictEqual(ok.counts.world, 3);
  assert.strictEqual(ok.subdivisionCode, 'TR-55');
  assert.strictEqual(ok.next, 60);

  assert.strictEqual(M.parseHeartbeat('too many heartbeats\n\n429').status, 429);
  assert.strictEqual(M.parseHeartbeat('unknown country\n\n400').status, 400);
  assert.strictEqual(M.parseHeartbeat('at capacity\n\n503').status, 503);
  // curl reports a network failure as 000, and anything unparseable is a
  // failure rather than a silently zeroed count.
  assert.strictEqual(M.parseHeartbeat('\n000').status, 0);
  assert.strictEqual(M.parseHeartbeat('').status, 0);
  assert.strictEqual(M.parseHeartbeat(null).status, 0);
  assert.strictEqual(M.parseHeartbeat('garbage\n200').status, 0);
  assert.strictEqual(M.parseHeartbeat('{"next":60}\n200').status, 0, 'a body without counts is not success');
}

// curl stops a response at MAX_RESPONSE_BYTES, so whatever arrives at the cap
// was cut mid-body. Parsing it would let an endpoint that pads a valid answer
// with an endless tail be believed, so it is rejected before parsing.
{
  const valid = '{"world":3,"country":2,"subdivision":1,"next":60}';
  assert.strictEqual(M.withinLimit(valid + '\n200'), true);
  assert.strictEqual(M.withinLimit(''), true);
  assert.strictEqual(M.withinLimit(null), true);
  assert.strictEqual(M.withinLimit(valid + ' '.repeat(M.MAX_RESPONSE_BYTES)), false);
  // The padded body is valid JSON, which is exactly why the cap and not the
  // parser has to be the thing that turns it away.
  assert.strictEqual(M.parseHeartbeat(valid + ' '.repeat(16) + '\n200').status, 200);
}

// The state machine that decides "counted" / "try again" / "offline". Three
// bugs came out of this branch, so it is exercised directly.
{
  const ok = { status: 200, counts: { world: 9, country: 4, subdivision: 2 },
               subdivisionCode: 'TR-55', next: 60 };

  // A landed beat is the only thing that produces counts.
  let step = M.nextHeartbeatState(M.heartbeatState(), ok);
  assert.strictEqual(step.state.connected, true);
  assert.strictEqual(step.state.hasCounts, true);
  assert.strictEqual(step.state.counts.world, 9);
  assert.strictEqual(step.interval, 60);
  assert.strictEqual(step.retry, false);

  // A 429 before any count must not report a confident zero: retry, stay
  // un-counted, and do not blame the server.
  step = M.nextHeartbeatState(M.heartbeatState(), { status: 429 });
  assert.strictEqual(step.retry, true);
  assert.strictEqual(step.state.hasCounts, false);
  assert.strictEqual(step.state.connected, false);
  assert.strictEqual(step.state.failures, 0, '429 is not a failure');
  assert.strictEqual(step.interval, 0, 'the poll interval is left alone');

  // A 429 with counts in hand keeps them and keeps the bar live, and still
  // retries so the beat actually lands.
  const live = M.nextHeartbeatState(M.heartbeatState(), ok).state;
  step = M.nextHeartbeatState(live, { status: 429 });
  assert.strictEqual(step.state.connected, true);
  assert.strictEqual(step.state.counts.world, 9);
  assert.strictEqual(step.retry, true);

  // Retries are bounded, so a permanently rate-limited client eventually
  // reports itself offline instead of polling forever.
  let s = M.heartbeatState();
  let retried = 0;
  for (let i = 0; i < 12; i++) {
    const r = M.nextHeartbeatState(s, { status: 429 });
    if (r.retry) retried++;
    s = r.state;
  }
  assert.strictEqual(retried, 5, `retried ${retried} times, want 5`);
  assert.strictEqual(s.connected, false);
  assert.ok(s.failures > 0, 'gives up into the failure path');

  // A backed-off interval is restored by the next beat that lands, so a
  // recovered server cannot leave the client polling every ten minutes while
  // its 180s presence quietly expires.
  let backed = M.heartbeatState();
  for (let i = 0; i < 4; i++) backed = M.nextHeartbeatState(backed, { status: 0 }).state;
  assert.strictEqual(M.nextInterval(60, backed.failures), 600);
  assert.strictEqual(M.nextHeartbeatState(backed, ok).interval, 60);

  // Transport failure keeps the last counts for the panel but drops the
  // connection, so the bar stops presenting them as current.
  step = M.nextHeartbeatState(live, { status: 0 });
  assert.strictEqual(step.state.connected, false);
  assert.strictEqual(step.state.counts.world, 9);
  assert.strictEqual(step.state.failures, 1);
  assert.strictEqual(step.interval, 120);

  assert.strictEqual(M.nextHeartbeatState(null, null).state.failures, 1);
}

// Display preferences must not cost a heartbeat.
{
  const base = { enabled: true, paused: false, country: 'TR', subdivision: 'TR-55' };
  assert.strictEqual(M.presenceChanged(base, Object.assign({}, base, { bar_scope: 'world' })), false);
  assert.strictEqual(M.presenceChanged(base, Object.assign({}, base, { bar_style: 'pulse-prefix' })), false);
  assert.strictEqual(M.presenceChanged(base, Object.assign({}, base, { subdivision: '' })), true);
  assert.strictEqual(M.presenceChanged(base, Object.assign({}, base, { country: 'DE' })), true);
  assert.strictEqual(M.presenceChanged(base, Object.assign({}, base, { paused: true })), true);
  assert.strictEqual(M.presenceChanged(base, Object.assign({}, base, { enabled: false })), true);
}

// The payload carries a location and nothing else (SPEC §14.1).
{
  assert.strictEqual(M.heartbeatBody('TR', 'TR-55'), '{"country":"TR","subdivision":"TR-55"}');
  assert.strictEqual(M.heartbeatBody('TR', ''), '{"country":"TR"}');
}

// The server dictates the cadence; failures back off.
{
  assert.strictEqual(M.nextInterval(60, 0), 60);
  assert.strictEqual(M.nextInterval(5, 0), 30, 'clamped up');
  assert.strictEqual(M.nextInterval(99999, 0), 600, 'clamped down');
  assert.strictEqual(M.nextInterval('nonsense', 0), 60);
  assert.strictEqual(M.nextInterval(60, 1), 120);
  assert.strictEqual(M.nextInterval(60, 9), 600, 'backoff is capped');
}

console.log('model.test.js: all assertions passed');
