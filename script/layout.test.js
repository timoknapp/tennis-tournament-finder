// Layout regression tests.
//
// These check geometry that unit tests cannot see and that screenshots only
// reveal by eye: symmetric panel margins, no accidental page scrolling, and a
// viewport meta tag the browser will actually honour.
//
// Run with: node script/layout.test.js
// Requires Playwright; skipped automatically when it is unavailable.

const assert = require('assert');
const fs = require('fs');
const http = require('http');
const path = require('path');

let chromium;
try {
    ({ chromium } = require('playwright'));
} catch (err) {
    console.log('Playwright not installed - skipping layout tests');
    process.exit(0);
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

const RESPONSE = JSON.stringify({
    tournaments: [{
        id: '1', title: 'Testturnier', url: '#', date: '22.08. bis 23.08.',
        location: 'Karlsruhe', organizer: 'TC Karlsruhe',
        lat: '49.0069', lon: '8.4037', entries: [],
    }],
    federations: [{ id: 'BAD', status: 'ok', count: 1 }],
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
    const browser = await chromium.launch();

    console.log('viewport meta');
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

    for (const [name, width, height] of VIEWPORTS) {
        console.log(`\n${name} (${width}x${height})`);

        const page = await browser.newPage({
            viewport: { width, height }, isMobile: true, hasTouch: true,
        });
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

        check('chips end flush on both sides', () => {
            assert.ok(Math.abs(panel.chipLeft - panel.chipRight) <= 4,
                `chip row inset left ${panel.chipLeft}px vs right ${panel.chipRight}px`);
        });

        await page.close();
    }

    await browser.close();
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
