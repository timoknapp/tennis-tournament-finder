// Unit tests for the list view's date parsing and sorting.
//
// Federations publish dates as free text in several formats, so this logic is
// easy to break and impossible to verify by looking at the map.
//
// Run with: node script/main.test.js

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

// main.js expects a browser. Load it in a sandbox with just enough DOM for the
// pure functions under test, and stop before the Leaflet setup runs.
const source = fs.readFileSync(path.join(__dirname, 'main.js'), 'utf8');
// Everything from the list view onwards is pure logic, safe to eval.
const listSection = source.slice(source.indexOf('// ===== Map markers ====='));

// Minimal Leaflet stub: the marker helpers only build icon descriptors.
const L = {
    icon: opts => ({ options: opts }),
    divIcon: opts => ({ options: opts }),
};

const sandbox = {
    console,
    Date,
    L,
    document: { getElementById: () => null, querySelectorAll: () => [] },
    window: {},
    urlGoogleQuery: 'https://maps.google.com/maps?q=',
    currentTournaments: [],
    markerById: new Map(),
    map: {},
    markers: {},
    setView: () => {},
};
vm.createContext(sandbox);
vm.runInContext(listSection, sandbox);

const { parseTournamentDate, compareTournaments, escapeHtml, isValidPlayerLK,
        tennisBallPin, clusterIcon } = sandbox;

let failures = 0;
function test(name, fn) {
    try {
        fn();
        console.log(`  ok  ${name}`);
    } catch (err) {
        failures++;
        console.error(`  FAIL ${name}\n       ${err.message}`);
    }
}

console.log('parseTournamentDate');

test('parses the old API range format', () => {
    const d = parseTournamentDate('22.08. bis 23.08.');
    assert.ok(d, 'expected a date');
    assert.strictEqual(d.getDate(), 22);
    assert.strictEqual(d.getMonth(), 7); // August
});

test('parses the new API format with weekday and year', () => {
    const d = parseTournamentDate('Sa, 15.8.2026');
    assert.strictEqual(d.getDate(), 15);
    assert.strictEqual(d.getMonth(), 7);
    assert.strictEqual(d.getFullYear(), 2026);
});

test('parses a range and returns the start date', () => {
    const d = parseTournamentDate('So, 16.8. – Fr, 21.8.2026');
    assert.strictEqual(d.getDate(), 16);
    assert.strictEqual(d.getMonth(), 7);
});

test('ignores trailing notes such as "abgesagt"', () => {
    const d = parseTournamentDate('Sa, 15.8.2026 abgesagt');
    assert.strictEqual(d.getDate(), 15);
    assert.strictEqual(d.getFullYear(), 2026);
});

test('returns null for unusable input', () => {
    for (const input of ['', null, undefined, 'demnächst', '99.99.']) {
        assert.strictEqual(parseTournamentDate(input), null, `input: ${input}`);
    }
});

test('rejects impossible months', () => {
    assert.strictEqual(parseTournamentDate('01.13.2026'), null);
});

console.log('compareTournaments');

test('sorts by date ascending', () => {
    const list = [
        { title: 'B', date: '22.08. bis 23.08.' },
        { title: 'A', date: '01.08. bis 02.08.' },
    ];
    list.sort((a, b) => compareTournaments(a, b, 'date'));
    assert.strictEqual(list[0].title, 'A');
});

test('puts entries with unparseable dates last', () => {
    const list = [
        { title: 'Unbekannt', date: 'wird noch bekannt gegeben' },
        { title: 'Konkret', date: '01.08. bis 02.08.' },
    ];
    list.sort((a, b) => compareTournaments(a, b, 'date'));
    assert.strictEqual(list[0].title, 'Konkret');
    assert.strictEqual(list[1].title, 'Unbekannt');
});

test('sorts by title using German collation', () => {
    const list = [
        { title: 'Zwickau Open' },
        { title: 'Ähringen Cup' },
        { title: 'Berlin Masters' },
    ];
    list.sort((a, b) => compareTournaments(a, b, 'title'));
    assert.deepStrictEqual(list.map(t => t.title),
        ['Ähringen Cup', 'Berlin Masters', 'Zwickau Open']);
});

test('sorts by organizer', () => {
    const list = [
        { organizer: 'TC Ulm' },
        { organizer: 'TC Aalen' },
    ];
    list.sort((a, b) => compareTournaments(a, b, 'organizer'));
    assert.strictEqual(list[0].organizer, 'TC Aalen');
});

test('falls back to the title when dates are equal', () => {
    const list = [
        { title: 'B', date: '01.08.' },
        { title: 'A', date: '01.08.' },
    ];
    list.sort((a, b) => compareTournaments(a, b, 'date'));
    assert.strictEqual(list[0].title, 'A');
});

test('handles missing fields without throwing', () => {
    const list = [{}, { title: 'A' }, { date: '01.08.' }];
    assert.doesNotThrow(() => list.sort((a, b) => compareTournaments(a, b, 'date')));
    assert.doesNotThrow(() => list.sort((a, b) => compareTournaments(a, b, 'title')));
});

console.log('escapeHtml');

test('escapes markup so tournament data cannot inject HTML', () => {
    assert.strictEqual(
        escapeHtml('<img src=x onerror="alert(1)">'),
        '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;');
});

test('escapes quotes and ampersands', () => {
    assert.strictEqual(escapeHtml(`Rot & Weiß "TC" 'X'`),
        'Rot &amp; Weiß &quot;TC&quot; &#39;X&#39;');
});

test('handles null and undefined', () => {
    assert.strictEqual(escapeHtml(null), '');
    assert.strictEqual(escapeHtml(undefined), '');
});

console.log('isValidPlayerLK');

test('accepts values across the LK scale', () => {
    for (const v of [1, 25, 12, '12', '12.5', '12,5', ' 7 ']) {
        assert.strictEqual(isValidPlayerLK(v), true, `should accept ${JSON.stringify(v)}`);
    }
});

test('rejects values outside the scale', () => {
    for (const v of [0, 0.9, 25.1, 30, -5]) {
        assert.strictEqual(isValidPlayerLK(v), false, `should reject ${JSON.stringify(v)}`);
    }
});

test('rejects empty and non-numeric input', () => {
    for (const v of ['', '   ', null, undefined, 'abc', 'LK']) {
        assert.strictEqual(isValidPlayerLK(v), false, `should reject ${JSON.stringify(v)}`);
    }
});

console.log('map markers');

test('pin svg is well formed and uses the accent colour', () => {
    const svg = tennisBallPin();
    assert.ok(svg.startsWith('<svg'), 'should be an svg element');
    assert.ok(svg.trim().endsWith('</svg>'), 'should be closed');
    assert.ok(svg.includes('#2f6f4e'), 'should use the accent colour');
    // The white outline is what lifts the pin off busy map tiles.
    assert.ok(svg.includes('stroke="#ffffff"'), 'should have a white outline');
});

test('pin scales without breaking the viewBox', () => {
    for (const size of [24, 34, 48]) {
        const svg = tennisBallPin({ size });
        assert.ok(svg.includes(`width="${size}"`), `size ${size} should be applied`);
        // A fixed viewBox is what keeps the artwork proportional at any size.
        assert.ok(svg.includes('viewBox="0 0 32 40"'), 'viewBox must stay constant');
    }
});

test('cluster label is escaped-safe and capped', () => {
    for (const [count, expected] of [[3, '>3<'], [42, '>42<'], [999, '>999<'], [1500, '>999+<']]) {
        const icon = clusterIcon(count);
        assert.ok(icon.options.html.includes(expected),
            `count ${count} should render as ${expected}`);
    }
});

test('cluster grows with the number of tournaments', () => {
    const small = clusterIcon(5).options.iconSize[0];
    const medium = clusterIcon(50).options.iconSize[0];
    const large = clusterIcon(500).options.iconSize[0];
    assert.ok(small < medium && medium < large, 'sizes should increase with count');
    // Capped so dense regions are not dominated by huge circles.
    assert.ok(large <= 60, 'largest cluster should stay reasonable');
});

test('cluster is anchored at its centre', () => {
    const icon = clusterIcon(10);
    const [w, h] = icon.options.iconSize;
    const [ax, ay] = icon.options.iconAnchor;
    assert.strictEqual(ax, w / 2, 'x anchor should be centred');
    assert.strictEqual(ay, h / 2, 'y anchor should be centred');
});

test('pin is anchored at its tip so it points at the venue', () => {
    // A centred anchor would place the pin's middle on the coordinates,
    // which shifts every tournament visibly north on the map.
    const svg = tennisBallPin({ size: 34 });
    assert.ok(svg.includes('width="34"'));
});

if (failures > 0) {
    console.error(`\n${failures} test(s) failed`);
    process.exit(1);
}
console.log('\nAll frontend tests passed');
