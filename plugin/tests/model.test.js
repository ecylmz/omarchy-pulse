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
