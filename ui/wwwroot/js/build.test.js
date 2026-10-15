import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const importMap = (fingerprint) =>
    `{"imports":{"./_framework/dotnet.js":"./_framework/dotnet.${fingerprint}.js"}}`;
const page = (fingerprint) =>
    `<!DOCTYPE html><html><head><script type="importmap">${importMap(fingerprint)}</script></head></html>`;

// What index.html loads by a fixed name, beside what it does not need to watch.
const LOADED = [
    ['href', '_content/MudBlazor/MudBlazor.min.css'],
    ['href', 'css/app.css'],
    ['href', 'css/tutorial.css'],
    ['src', 'js/three.min.js'],
    ['src', 'js/theme.js'],
    ['src', 'js/tutorial.js'],
    ['src', 'js/tipfit.js'],
    ['src', 'js/build.js'],
    ['src', '_framework/blazor.webassembly.66stpp682q.js'],
];

// build.js is a plain script, like theme.js: index.html loads it before Blazor exists, so it
// publishes onto window, and evaluating the source is the only way to reach it from here.
function load({ served = () => page('aaa'), dates = () => ({}), unreachable = false } = {}) {
    const asked = [];
    const reloads = [];
    let pressed;
    const notice = {
        hidden: true,
        querySelector: () => ({ addEventListener: (_, fn) => { pressed = fn; } }),
    };
    const document = {
        hidden: false,
        getElementById: (id) => (id === 'abom-stale-build' ? notice : null),
        querySelector: (selector) => (selector === 'script[type="importmap"]' ? { textContent: `\n${importMap('aaa')}\n` } : null),
        querySelectorAll: () => LOADED.map(([attribute, value]) => ({
            getAttribute: (name) => (name === attribute ? value : null),
        })),
        addEventListener: () => {},
    };
    const window = { addEventListener: () => {} };
    const fetch = async (url, init = {}) => {
        asked.push(`${init.method ?? 'GET'} ${url}`);
        if (unreachable) throw new TypeError('Failed to fetch');
        if (init.method === 'HEAD') {
            const date = dates()[url];
            return { ok: true, headers: { get: (name) => (name.toLowerCase() === 'last-modified' ? date ?? null : null) } };
        }
        return { ok: true, text: async () => served() };
    };
    const location = { reload: () => reloads.push(true) };
    const source = readFileSync(fileURLToPath(new URL('./build.js', import.meta.url)), 'utf8');
    new Function('window', 'document', 'fetch', 'location', 'setInterval', source)(
        window, document, fetch, location, () => 0);
    return { build: window.abomBuild, notice, asked, reloads, press: () => pressed() };
}

const MONDAY = 'Mon, 12 Oct 2026 07:41:20 GMT';
const TUESDAY = 'Tue, 13 Oct 2026 07:41:20 GMT';

test('a page the server still serves says nothing', async () => {
    const { build, notice } = load({ dates: () => ({ 'css/app.css': MONDAY }) });
    await build.ready;

    assert.equal(await build.check(), false);
    assert.equal(notice.hidden, true);
});

// The reported failure: `make restart` replaced the server, and a page loaded the night before
// kept running the build that had no Audit log tab, so the tab looked missing rather than unloaded.
test('a page running an earlier build than the server says so', async () => {
    const { build, notice } = load({ served: () => page('bbb') });
    await build.ready;

    assert.equal(await build.check(), true);
    assert.equal(notice.hidden, false);
});

// A stylesheet is not fingerprinted, so a change to it alone never reaches the import map.
test('a restyled interface is a newer build too', async () => {
    let date = MONDAY;
    const { build, notice } = load({ dates: () => ({ 'css/app.css': date }) });
    await build.ready;
    date = TUESDAY;

    assert.equal(await build.check(), true);
    assert.equal(notice.hidden, false);
});

test('only first-party files loaded by a fixed name are watched', async () => {
    const { build, asked } = load();
    await build.ready;

    assert.deepEqual(asked.filter((a) => a.startsWith('HEAD')).sort(),
        ['HEAD css/app.css', 'HEAD css/tutorial.css', 'HEAD js/build.js', 'HEAD js/theme.js', 'HEAD js/tipfit.js', 'HEAD js/tutorial.js']);
});

// A server restarting refuses the connection for a moment; that is not news about the build.
test('a server that cannot be reached is not a newer build', async () => {
    const { build, notice } = load({ unreachable: true });
    await build.ready;

    assert.equal(await build.check(), false);
    assert.equal(notice.hidden, true);
});

// A proxy's error page, or anything that is not the interface's own entry point, has no import
// map, and the absence of one says nothing about which build the server holds.
test('an answer that is not the interface page proves nothing', async () => {
    const { build, notice } = load({ served: () => '<html><body>Bad gateway</body></html>' });
    await build.ready;

    assert.equal(await build.check(), false);
    assert.equal(notice.hidden, true);
});

test('a stylesheet answered without a date proves nothing', async () => {
    let date = MONDAY;
    const { build } = load({ dates: () => ({ 'css/app.css': date }) });
    await build.ready;
    date = undefined;

    assert.equal(await build.check(), false);
});

test('the notice reloads the page into the build the server serves', async () => {
    const { build, reloads, press } = load({ served: () => page('bbb') });
    await build.ready;
    await build.check();

    press();
    assert.equal(reloads.length, 1);
});
