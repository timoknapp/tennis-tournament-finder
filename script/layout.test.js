// Layout regression tests.
//
// These check geometry that unit tests cannot see and that screenshots only
// reveal by eye: symmetric panel margins, no accidental page scrolling, and a
// viewport meta tag the browser will actually honour.
//
// They run in Chromium and WebKit, because the two engines disagree about
// form control sizing, flexbox rounding and viewport units, and those
// disagreements are what reach a phone.
//
// Known limit: this is WebKitGTK. It shares Safari's engine but not the
// control widgets iOS ships, so a date input here is sized from the
// stylesheet rather than from a native picker. The iOS-only width bug that
// motivated these tests is therefore still not reproducible on Linux - the
// assertions catch it by checking the CSS contract (appearance is reset,
// widths and edges agree) rather than by rendering the native control.
//
// Run with: node script/layout.test.js
// Requires Playwright; skipped automatically when it is unavailable.

const assert = require('assert');
const fs = require('fs');
const http = require('http');
const path = require('path');

let playwright;
try {
    playwright = require('playwright');
} catch (err) {
    console.log('Playwright not installed - skipping layout tests');
    process.exit(0);
}

// Chromium and WebKit disagree about form control sizing, flexbox rounding and
// viewport units, and those disagreements are what reach a phone. WebKit here
// is WebKitGTK, which shares Safari's engine but not its iOS control widgets,
// so it narrows the gap rather than closing it.
async function engines() {
    const available = [];
    for (const name of ['chromium', 'webkit']) {
        if (!playwright[name]) continue;
        try {
            const browser = await playwright[name].launch();
            await browser.close();
            available.push(name);
        } catch (err) {
            console.log(`  (${name} unavailable, skipping: ${err.message.split('\n')[0]})`);
        }
    }
    return available;
}

const ROOT = path.join(__dirname, '..');
const PORT = 8123;

const MIME = {
    '.html': 'text/html; charset=utf-8',
    '.js': 'text/javascript; charset=utf-8',
    '.css': 'text/css; charset=utf-8',
    '.png': 'image/png',
    '.ico': 'image/x-icon',
};

// 1x1 transparent PNG, stands in for map tiles and icons.
const TILE = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk' +
    'YPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64');

// Enough results that the list overflows every viewport under test; the
// scrolling assertions are meaningless against a list that fits on screen.
const TOURNAMENTS = Array.from({ length: 20 }, (_, i) => ({
    id: String(i + 1),
    title: `Testturnier ${i + 1}`,
    url: '#',
    date: '22.08. bis 23.08.',
    location: 'Karlsruhe',
    organizer: `TC Karlsruhe ${i + 1}`,
    lat: String(49.0069 + i * 0.02),
    lon: String(8.4037 + i * 0.02),
    entries: [{ competition: 'Herren Einzel', skill_level: 'LK 12,0' }],
}));

const RESPONSE = JSON.stringify({
    tournaments: TOURNAMENTS,
    federations: [{ id: 'BAD', status: 'ok', count: TOURNAMENTS.length }],
    partial: false,
});

function serve() {
    return http.createServer((req, res) => {
        const url = new URL(req.url, 'http://localhost');
        const file = url.pathname === '/' ? '/index.html' : url.pathname;
        const full = path.join(ROOT, file);
        if (!fs.existsSync(full) || fs.statSync(full).isDirectory()) {
            res.writeHead(404).end();
            return;
        }
        res.writeHead(200, { 'Content-Type': MIME[path.extname(full)] || 'application/octet-stream' });
        fs.createReadStream(full).pipe(res);
    });
}

let failures = 0;
function check(name, fn) {
    try {
        fn();
        console.log(`  ok  ${name}`);
    } catch (err) {
        failures++;
        console.error(`  FAIL ${name}\n       ${err.message}`);
    }
}

// Phone sizes including the shorter viewport mobile Safari reports while its
// address bar is visible, which is where the scrolling bug appeared.
const VIEWPORTS = [
    ['iPhone SE', 375, 667],
    ['iPhone SE, browser chrome', 375, 553],
    ['iPhone 12', 390, 844],
    ['iPhone 12, browser chrome', 390, 664],
    ['iPhone 14 Pro Max', 430, 932],
    ['Pixel 7', 412, 915],
];

(async () => {
    const server = serve();
    await new Promise(r => server.listen(PORT, r));

    const names = await engines();
    if (names.length === 0) {
        console.log('No browser engine available - skipping layout tests');
        server.close();
        process.exit(0);
    }
    console.log(`engines: ${names.join(', ')}`);

    console.log('\nviewport meta');
    const html = fs.readFileSync(path.join(ROOT, 'index.html'), 'utf8');
    check('viewport is declared on its own meta element', () => {
        // A single element cannot carry both charset and name: the browser
        // keeps the charset and drops the viewport, so the page stays
        // zoomable and scrollable on phones.
        const combined = /<meta[^>]*charset[^>]*name=["']viewport["']/i.test(html);
        assert.ok(!combined, 'charset and viewport must not share one meta tag');
        assert.ok(/<meta\s+name=["']viewport["']/i.test(html), 'viewport meta must exist');
    });

    check('viewport allows zooming', () => {
        const meta = html.match(/<meta\s+name=["']viewport["'][^>]*>/i)[0];
        // Blocking zoom fails WCAG 1.4.4 and is unnecessary here.
        assert.ok(!/user-scalable\s*=\s*no/i.test(meta), 'zoom must not be disabled');
        assert.ok(!/maximum-scale\s*=\s*1/i.test(meta), 'maximum-scale must not pin zoom');
    });

    for (const engine of names) {
    const browser = await playwright[engine].launch();
    for (const [name, width, height] of VIEWPORTS) {
        console.log(`\n[${engine}] ${name} (${width}x${height})`);

        // A fresh context per viewport. browser.newPage() reuses the default
        // context, so the service worker registered by one run served cached
        // responses to the next; under WebKit that produced failures which
        // accumulated across viewports and looked like flakiness.
        //
        // WebKitGTK also rejects the mobile emulation flags Chromium accepts.
        const context = await browser.newContext(engine === 'webkit'
            ? { viewport: { width, height }, hasTouch: true, serviceWorkers: 'block' }
            : { viewport: { width, height }, isMobile: true, hasTouch: true, serviceWorkers: 'block' });
        const page = await context.newPage();
        await page.route('**/ttf**', r =>
            r.fulfill({ status: 200, contentType: 'application/json', body: RESPONSE }));
        await page.route('**tile**', r =>
            r.fulfill({ status: 200, contentType: 'image/png', body: TILE }));
        await page.route('**/images/**', r =>
            r.fulfill({ status: 200, contentType: 'image/png', body: TILE }));

        await page.goto(`http://localhost:${PORT}/index.html`, { waitUntil: 'domcontentloaded' });
        await page.waitForTimeout(700);

        const closed = await page.evaluate(() => ({
            x: document.documentElement.scrollWidth - document.documentElement.clientWidth,
            y: document.documentElement.scrollHeight - document.documentElement.clientHeight,
        }));

        check('map view does not scroll the document', () => {
            // The map handles panning itself; a scrolling document underneath
            // makes the whole page feel loose.
            assert.strictEqual(closed.x, 0, `horizontal overflow of ${closed.x}px`);
            assert.strictEqual(closed.y, 0, `vertical overflow of ${closed.y}px`);
        });

        const initialMarkers = await page.evaluate(
            () => document.querySelectorAll('.leaflet-marker-icon').length);
        const initialFilterDisplay = await page.evaluate(
            () => getComputedStyle(document.getElementById('filterContainer')).display);

        // The filter action moved out of the title row into its own control.
        //
        // The panel must be closed before this taps to open it. The initial
        // load renders asynchronously, and on a slow run the auto-close had not
        // yet applied when the tap landed, so the tap closed the panel instead
        // and every following assertion failed. Waiting for the known start
        // state removes the race rather than papering over it with a delay.
        await page.waitForFunction(
            () => getComputedStyle(document.getElementById('filterContainer')).display === 'none',
            null, { timeout: 5000 }).catch(() => {});

        await page.click('#filterFab', { force: true });
        await page.waitForFunction(
            () => getComputedStyle(document.getElementById('filterContainer')).display !== 'none',
            null, { timeout: 5000 }).catch(() => {});
        await page.waitForTimeout(200);
        const filterAfterFirstTap = await page.evaluate(
            () => getComputedStyle(document.getElementById('filterContainer')).display);

        const sheet = await page.evaluate(() => {
            const panel = document.getElementById('filterContainer');
            const rect = panel.getBoundingClientRect();
            const scrim = document.getElementById('sheetScrim');
            const fab = document.getElementById('filterFab');

            // Visibility is not usability: the sheet was fully rendered while
            // the scrim sat on top of it and swallowed every tap. Only
            // elementFromPoint reveals that.
            const unreachable = [];
            let probed = 0;
            ['#dateFrom', '#dateTo', '#compType', '#playerLK', '.sheetDone', '.checkboxLabel']
                .forEach(sel => {
                    const el = document.querySelector(sel);
                    if (!el) return;
                    const b = el.getBoundingClientRect();
                    // Off-screen controls cannot be probed, but they must not
                    // silently reduce this to a no-op either: `probed` is
                    // asserted separately.
                    if (!b.width || b.top < 0 || b.bottom > window.innerHeight) return;
                    probed++;
                    const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
                    // A label wrapping its input counts as reached; an overlay
                    // in front of it does not.
                    if (!(hit === el || el.contains(hit))) {
                        unreachable.push(`${sel} blocked by ${(hit && (hit.id || hit.className)) || 'nothing'}`);
                    }
                });

            // The tab bar must not float above the sheet that covers it.
            const tabbar = document.querySelector('.viewToggle').getBoundingClientRect();
            const atTabbar = document.elementFromPoint(tabbar.left + 40, tabbar.top + tabbar.height / 2);
            const tabbarOnTop = !!(atTabbar && atTabbar.nodeType === 1 && atTabbar.closest('.viewToggle'));

            return {
                topGap: Math.round(rect.top),
                scrimShown: scrim ? !scrim.hidden : false,
                fabHidden: getComputedStyle(fab).display === 'none',
                doneVisible: !!document.querySelector('.sheetDone'),
                unreachable,
                probed,
                tabbarOnTop,
            };
        });

        check('sheet controls actually receive taps', () => {
            // .sideBar carries a z-index, making it a stacking context: its
            // children cannot paint above a sibling scrim however high their
            // own z-index. Every control was rendered and visible but dead,
            // which is why a visibility-only assertion missed it.
            //
            // The probe count is asserted too: if the sheet fails to open, its
            // controls fall outside the viewport and this would otherwise pass
            // by examining nothing.
            assert.ok(sheet.probed >= 3,
                `only ${sheet.probed} control(s) on screen; the sheet did not open`);
            assert.strictEqual(sheet.unreachable.length, 0,
                `unreachable: ${sheet.unreachable.join('; ')}`);
        });

        check('tab bar does not float above the open sheet', () => {
            assert.ok(!sheet.tabbarOnTop,
                'the tab bar paints over the sheet that covers it');
        });

        check('open sheet leaves the map visible above it', () => {
            // A sheet filling the screen is a full-page takeover; keeping the
            // map in view is what makes filtering feel in-context.
            assert.ok(sheet.topGap > 40,
                `sheet starts ${sheet.topGap}px from the top, covering the view`);
        });

        check('sheet dims the map and offers a way out', () => {
            assert.ok(sheet.scrimShown, 'scrim should be shown behind the sheet');
            assert.ok(sheet.doneVisible, 'sheet needs its own dismiss control');
        });

        check('filter button hides while its sheet is open', () => {
            assert.ok(sheet.fabHidden, 'the filter button overlaps the open sheet');
        });

        await page.evaluate(() => {
            document.getElementById('filterContainer').style.display = 'block';
        });
        await page.waitForTimeout(250);

        const open = await page.evaluate(() => {
            const vw = window.innerWidth;
            const panel = document.getElementById('filterContainer').getBoundingClientRect();
            const input = document.getElementById('dateFrom').getBoundingClientRect();
            const chip = document.querySelector('.checkboxLabel').getBoundingClientRect();
            return {
                panelLeft: Math.round(panel.left),
                panelRight: Math.round(vw - panel.right),
                insetLeft: Math.round(input.left - panel.left),
                insetRight: Math.round(panel.right - input.right),
                chipInset: Math.round(chip.left - panel.left),
                overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth,
            };
        });

        check('panel margins are symmetric', () => {
            assert.ok(Math.abs(open.panelLeft - open.panelRight) <= 2,
                `left ${open.panelLeft}px vs right ${open.panelRight}px`);
        });

        check('content is evenly inset inside the panel', () => {
            assert.ok(Math.abs(open.insetLeft - open.insetRight) <= 2,
                `inset left ${open.insetLeft}px vs right ${open.insetRight}px`);
        });

        check('content does not touch the panel edge', () => {
            // Without a real inset the card looks like the text is falling out
            // of it, which is what the vh-based padding used to cause.
            assert.ok(open.insetLeft >= 10, `only ${open.insetLeft}px of padding`);
            assert.ok(open.chipInset >= 10, `chips only ${open.chipInset}px from the edge`);
        });

        check('open panel does not cause horizontal scrolling', () => {
            assert.strictEqual(open.overflowX, 0, `horizontal overflow of ${open.overflowX}px`);
        });

        const panel = await page.evaluate(() => {
            const el = document.getElementById('filterContainer');
            const cs = getComputedStyle(el);
            const rect = el.getBoundingClientRect();
            const innerLeft = rect.left + parseFloat(cs.paddingLeft);
            const innerRight = rect.right - parseFloat(cs.paddingRight);
            const chips = [...document.querySelectorAll('.checkboxLabel')];
            const label = document.querySelector('#filterContainer label').getBoundingClientRect();
            return {
                overflowX: el.scrollWidth - el.clientWidth,
                overflowXStyle: cs.overflowX,
                labelInset: Math.round(label.left - innerLeft),
                chipLeft: Math.round(Math.min(...chips.map(c => c.getBoundingClientRect().left)) - innerLeft),
                chipRight: Math.round(innerRight - Math.max(...chips.map(c => c.getBoundingClientRect().right))),
            };
        });

        check('panel itself does not scroll sideways', () => {
            // The panel scrolls vertically by design. overflow: auto enables
            // both axes, so a child even a fraction wider than the content box
            // makes the card draggable sideways and slides the labels out of
            // view.
            assert.strictEqual(panel.overflowXStyle, 'hidden',
                `overflow-x is ${panel.overflowXStyle}, must be hidden`);
            assert.strictEqual(panel.overflowX, 0,
                `panel content overflows by ${panel.overflowX}px`);
        });

        check('labels are not clipped at the panel edge', () => {
            assert.ok(panel.labelInset >= -1,
                `label starts ${panel.labelInset}px inside the padding box`);
        });

        const fields = await page.evaluate(() => {
            const ids = ['dateFrom', 'dateTo', 'compType', 'playerLK'];
            const boxes = ids.map(id => {
                const el = document.getElementById(id);
                const r = el.getBoundingClientRect();
                const cs = getComputedStyle(el);
                return {
                    id, width: r.width, left: r.left, right: r.right,
                    appearance: cs.webkitAppearance || cs.appearance,
                    fontSize: parseFloat(cs.fontSize),
                };
            });
            const submit = document.querySelector('.submitBtn').getBoundingClientRect();
            return { boxes, submitWidth: submit.width };
        });

        check('all form fields are the same width', () => {
            // Safari sizes date inputs from their native control rather than
            // the author's box model, so they render wider than the select and
            // text input beside them and push the panel into scrolling.
            const widths = fields.boxes.map(b => b.width);
            const spread = Math.max(...widths) - Math.min(...widths);
            const detail = fields.boxes.map(b => `${b.id}=${Math.round(b.width)}`).join(' ');
            assert.ok(spread <= 1, `widths differ by ${spread.toFixed(1)}px (${detail})`);
        });

        check('form fields share both edges', () => {
            const lefts = fields.boxes.map(b => b.left);
            const rights = fields.boxes.map(b => b.right);
            assert.ok(Math.max(...lefts) - Math.min(...lefts) <= 1, 'left edges must line up');
            assert.ok(Math.max(...rights) - Math.min(...rights) <= 1, 'right edges must line up');
        });

        check('submit button matches the field width', () => {
            const width = fields.boxes[0].width;
            assert.ok(Math.abs(fields.submitWidth - width) <= 1,
                `button ${Math.round(fields.submitWidth)}px vs field ${Math.round(width)}px`);
        });

        check('native control appearance is reset', () => {
            // Without this the browser imposes its own sizing, which is how the
            // widths diverged on iOS while looking correct in Chromium.
            for (const box of fields.boxes) {
                assert.strictEqual(box.appearance, 'none',
                    `${box.id} has appearance: ${box.appearance}`);
            }
        });

        check('fields are large enough not to trigger iOS focus zoom', () => {
            // Safari zooms the page when a field with a font-size below 16px
            // gains focus, leaving the layout shifted afterwards.
            for (const box of fields.boxes) {
                assert.ok(box.fontSize >= 16,
                    `${box.id} font-size is ${box.fontSize}px, below the 16px threshold`);
            }
        });

        check('chips end flush on both sides', () => {
            assert.ok(Math.abs(panel.chipLeft - panel.chipRight) <= 4,
                `chip row inset left ${panel.chipLeft}px vs right ${panel.chipRight}px`);
        });

        const controls = await page.evaluate(() => {
            const toggle = document.querySelector('.viewToggle').getBoundingClientRect();
            const panel = document.getElementById('filterContainer').getBoundingClientRect();
            const tabs = [...document.querySelectorAll('.viewToggleBtn')];
            return {
                zoomButtons: document.querySelectorAll('.leaflet-control-zoom').length,
                toggleRole: document.querySelector('.viewToggle').getAttribute('role'),
                toggleWidth: toggle.width,
                toggleBottomGap: Math.round(window.innerHeight - toggle.bottom),
                smallestTab: Math.min(...tabs.map(t => t.getBoundingClientRect().height)),
                tabHeight: tabs[0].getBoundingClientRect().height,
                iconSize: (document.querySelector('.tabIcon') || { getBoundingClientRect: () => ({ height: 0 }) })
                    .getBoundingClientRect().height,
                viewportWidth: window.innerWidth,
                panelBottom: panel.bottom,
                toggleTop: toggle.top,
                viewportHeight: window.innerHeight,
            };
        });

        check('map has no zoom buttons', () => {
            // Pinch, double-tap and the wheel all zoom, so the buttons only
            // occupied the corner the filter panel needs.
            assert.strictEqual(controls.zoomButtons, 0, 'zoom control should be disabled');
        });

        check('view toggle is a tab bar across the bottom', () => {
            // Two destinations, full width, anchored to the bottom edge where
            // the thumb rests.
            assert.strictEqual(controls.toggleRole, 'tablist',
                'the view switcher should expose a tablist');
            assert.ok(controls.toggleWidth >= controls.viewportWidth - 1,
                `tab bar is ${Math.round(controls.toggleWidth)}px of ${controls.viewportWidth}px`);
            assert.ok(controls.toggleBottomGap <= 1,
                `tab bar sits ${controls.toggleBottomGap}px above the bottom edge`);
        });

        check('tabs are large enough to hit', () => {
            assert.ok(controls.smallestTab >= 44,
                `smallest tab is ${Math.round(controls.smallestTab)}px tall`);
        });

        check('tab bar uses native metrics', () => {
            // iOS tab bars are 49pt with the home indicator area added below,
            // not padded inside. A taller bar reads as a web page imitating an
            // app, and leaves a dead gap under the labels.
            assert.ok(controls.tabHeight <= 52,
                `tabs are ${Math.round(controls.tabHeight)}px tall, above the 49pt platform metric`);
            assert.ok(controls.iconSize >= 24 && controls.iconSize <= 28,
                `tab icons are ${Math.round(controls.iconSize)}px, outside the 25pt convention`);
        });

        check('open panel fits the visible viewport', () => {
            // A max-height in vh is measured against the viewport with the
            // browser chrome hidden, so the panel could extend past what the
            // user can see and clip its last line. The sheet is anchored to the
            // bottom edge, so it ends exactly there rather than above the tabs.
            assert.ok(controls.panelBottom <= controls.viewportHeight + 1,
                `panel ends ${Math.round(controls.panelBottom - controls.viewportHeight)}px below the fold`);
        });

        // The list view is a normal scrolling page; the map view is not. Both
        // states have to work with the filter panel open, which is where the
        // scroll containers previously fought each other.
        await page.click('.submitBtn', { force: true });
        await page.waitForTimeout(900);
        await page.click('#viewListBtn', { force: true });
        await page.waitForTimeout(500);

        const list = await page.evaluate(async () => {
            const scroller = document.scrollingElement;
            const reach = scroller.scrollHeight - scroller.clientHeight;
            scroller.scrollTop = 99999;
            await new Promise(r => setTimeout(r, 250));
            const scrolled = scroller.scrollTop;
            const items = [...document.querySelectorAll('.tournament-item')];
            const last = items[items.length - 1].getBoundingClientRect();
            const toggle = document.querySelector('.viewToggle').getBoundingClientRect();
            const nested = [...document.querySelectorAll('.list-view, .filter')]
                .filter(el => el.scrollHeight > el.clientHeight + 2).length;
            // Anything fixed that sits on top of a result is a defect.
            let covered = 0;
            const floating = [...document.querySelectorAll('.filterFab, .viewToggle')]
                .filter(el => getComputedStyle(el).position === 'fixed');
            document.querySelectorAll('.tournament-item a, .tournament-item button').forEach(el => {
                const r = el.getBoundingClientRect();
                if (!r.width) return;
                if (floating.some(f => {
                    const c = f.getBoundingClientRect();
                    return !(r.right < c.left || r.left > c.right || r.bottom < c.top || r.top > c.bottom);
                })) covered++;
            });

            return {
                reach, scrolled, nested, covered,
                lastHiddenByToggle: last.bottom > toggle.top,
                toggleWidth: toggle.width,
                viewportWidth: window.innerWidth,
            };
        });

        check('list view scrolls', () => {
            assert.ok(list.reach > 0, 'list should be taller than the viewport');
            assert.ok(list.scrolled > 0, 'document did not scroll');
        });

        check('list view has a single scroll container', () => {
            // Stacking the document, the filter panel and the list meant a
            // swipe could land on the wrong one and appear to do nothing.
            assert.strictEqual(list.nested, 0,
                `${list.nested} nested scroll container(s) inside the page`);
        });

        check('last result is not hidden behind the toggle', () => {
            assert.ok(!list.lastHiddenByToggle, 'the floating toggle covers the last result');
        });

        check('tab bar spans the list view too', () => {
            assert.ok(list.toggleWidth >= list.viewportWidth - 1,
                'the tab bar should stay full width in the list');
        });

        check('nothing floats over the results', () => {
            // A control floating above a scrolling list eventually covers a
            // link in it; measured against the real list, it covered
            // "Auf Karte zeigen".
            assert.strictEqual(list.covered, 0,
                `${list.covered} interactive element(s) sit under floating chrome`);
        });

        await page.close();
        await context.close();
    }
    await browser.close();
    }

    server.close();

    if (failures > 0) {
        console.error(`\n${failures} layout check(s) failed`);
        process.exit(1);
    }
    console.log('\nAll layout tests passed');
})().catch(err => {
    console.error('layout tests errored:', err.message);
    process.exit(1);
});
