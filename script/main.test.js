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
const listSection = source.slice(source.indexOf('// ===== List view ====='));

const sandbox = {
    console,
    Date,
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

const { parseTournamentDate, compareTournaments, escapeHtml, isValidPlayerLK } = sandbox;

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

if (failures > 0) {
    console.error(`\n${failures} test(s) failed`);
    process.exit(1);
}
console.log('\nAll frontend tests passed');
