// Service worker update tests.
//
// A merged fix that never reaches the device is not fixed. These check the
// update path itself: a returning visitor must get the new build, a first-time
// visitor must not be reloaded for nothing, and neither may end up in a loop.
//
// Run with: node script/sw-update.test.js
// Requires Playwright and a git worktree of an older build; both are optional,
// and the suite skips itself when either is missing.

const assert = require('assert');
const { execFileSync } = require('child_process');
const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');

let chromium;
try {
    ({ chromium } = require('playwright'));
} catch (err) {
    console.log('Playwright not installed - skipping service worker tests');
    process.exit(0);
}

const ROOT = path.join(__dirname, '..');
const PORT = 8134;

const MIME = {
    '.html': 'text/html; charset=utf-8',
    '.js': 'text/javascript; charset=utf-8',
    '.css': 'text/css; charset=utf-8',
    '.png': 'image/png',
    '.json': 'application/json',
};

// The previous release, extracted from git. Comparing against the working tree
// is what makes this meaningful: the update logic has to be delivered *by* the
// old build, which cannot know about it.
function checkoutPrevious() {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ttf-prev-'));
    try {
        // Pick the most recent commit whose markup predates the current UI,
        // rather than counting commits back. Cache-version bumps and unrelated
        // changes land constantly, so a fixed offset would silently start
        // comparing the build against itself.
        const revs = execFileSync('git', ['log', '-30', '--format=%H'],
            { cwd: ROOT, encoding: 'utf8' }).trim().split('\n');
        let rev = null;
        for (const candidate of revs) {
            const markup = execFileSync('git', ['show', `${candidate}:index.html`],
                { cwd: ROOT, encoding: 'utf8', maxBuffer: 1 << 26 });
            if (!markup.includes('tabIcon')) {
                rev = candidate;
                break;
            }
        }
        if (!rev) return null;
        const tar = execFileSync('git', ['archive', rev], { cwd: ROOT, maxBuffer: 1 << 28 });
        const tarPath = path.join(dir, 'prev.tar');
        fs.writeFileSync(tarPath, tar);
        execFileSync('tar', ['-xf', tarPath, '-C', dir]);
        fs.unlinkSync(tarPath);
        return dir;
    } catch (err) {
        console.log(`Could not extract the previous build (${err.message.split('\n')[0]})`);
        return null;
    }
}

let serveFrom = ROOT;

function serve() {
    return http.createServer((req, res) => {
        const url = new URL(req.url, 'http://localhost');
        if (url.pathname.startsWith('/ttf')) {
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end('{"tournaments":[],"federations":[],"partial":false}');
            return;
        }
        const file = url.pathname === '/' ? '/index.html' : url.pathname;
        const full = path.join(serveFrom, file);
        if (!fs.existsSync(full) || fs.statSync(full).isDirectory()) {
            res.writeHead(404).end();
            return;
        }
        res.writeHead(200, {
            'Content-Type': MIME[path.extname(full)] || 'application/octet-stream',
            // GitHub Pages serves with max-age=600; the worker works around it.
            'Cache-Control': 'max-age=600',
        });
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

// Marker for the current build: present in the new UI, absent in older ones.
const NEW_UI = () => !!document.querySelector('.tabIcon');

(async () => {
    const previous = checkoutPrevious();
    if (!previous) {
        console.log('No previous build available - skipping service worker tests');
        process.exit(0);
    }

    const server = serve();
    await new Promise(r => server.listen(PORT, r));
    const browser = await chromium.launch();
    const url = `http://localhost:${PORT}/index.html`;

    // ---- A returning visitor gets the new build without being asked.
    console.log('returning visitor');
    serveFrom = previous;
    let context = await browser.newContext({ viewport: { width: 390, height: 664 } });
    let page = await context.newPage();
    await page.goto(url, { waitUntil: 'load' });
    await page.waitForTimeout(2500);

    const sawOldBuild = await page.evaluate(NEW_UI);

    serveFrom = ROOT;                       // deploy
    let navigations = 0;
    page.on('framenavigated', f => { if (f === page.mainFrame()) navigations++; });
    await page.reload({ waitUntil: 'load' });
    // Deliberately no interaction: the update must not depend on one.
    await page.waitForTimeout(8000);

    const updated = await page.evaluate(NEW_UI);
    const returningNavigations = navigations;

    check('the previous build is actually older', () => {
        assert.ok(!sawOldBuild,
            'the extracted build already contains the current UI; the comparison proves nothing');
    });

    check('a returning visitor receives the new build untouched', () => {
        // The old page cannot know about any change to the update logic, so the
        // worker has to activate itself. Requiring a tap meant a merged fix
        // could sit undelivered indefinitely.
        assert.ok(updated, 'still serving the old build after a reload');
    });

    check('updating settles instead of looping', () => {
        // skipWaiting plus a controllerchange reload is a classic reload loop.
        assert.ok(returningNavigations <= 3,
            `${returningNavigations} navigations; the page is reloading repeatedly`);
    });

    await context.close();

    // ---- A first-time visitor should not be reloaded at all.
    console.log('\nfirst-time visitor');
    context = await browser.newContext({ viewport: { width: 390, height: 664 } });
    page = await context.newPage();
    let firstNavigations = 0;
    page.on('framenavigated', f => { if (f === page.mainFrame()) firstNavigations++; });
    await page.goto(url, { waitUntil: 'load' });
    await page.waitForTimeout(6000);
    const firstVisitUi = await page.evaluate(NEW_UI);

    check('a first-time visitor is not reloaded', () => {
        // There is no previous worker to replace, so a reload would refresh a
        // page the user has only just opened.
        assert.strictEqual(firstNavigations, 1,
            `${firstNavigations} navigations on a first visit`);
    });

    check('a first-time visitor sees the current build', () => {
        assert.ok(firstVisitUi, 'first visit did not render the current UI');
    });

    await context.close();
    await browser.close();
    server.close();
    fs.rmSync(previous, { recursive: true, force: true });

    if (failures > 0) {
        console.error(`\n${failures} service worker check(s) failed`);
        process.exit(1);
    }
    console.log('\nAll service worker tests passed');
})().catch(err => {
    console.error('service worker tests errored:', err.message);
    process.exit(1);
});
