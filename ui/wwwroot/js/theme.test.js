import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// theme.js is a plain script, not a module: index.html loads it before Blazor exists so it
// publishes onto window. Evaluating the source is the only way to reach it from here.
function load() {
    const attributes = {};
    const stored = {};
    const window = {
        matchMedia: () => ({ matches: false }),
    };
    const document = {
        documentElement: {
            setAttribute: (name, value) => { attributes[name] = value; },
        },
    };
    const localStorage = {
        getItem: (k) => (k in stored ? stored[k] : null),
        setItem: (k, v) => { stored[k] = v; },
    };
    const source = readFileSync(fileURLToPath(new URL('./theme.js', import.meta.url)), 'utf8');
    new Function('window', 'document', 'localStorage', source)(window, document, localStorage);
    return { theme: window.abomTheme, attributes, stored };
}

test('the resolved scheme reaches the document either way', () => {
    const { theme, attributes } = load();

    theme.applyResolved(true);
    assert.equal(attributes['data-theme'], 'dark');

    theme.applyResolved(false);
    assert.equal(attributes['data-theme'], 'light');
});

test('saving a preference that is not the theme leaves the scheme alone', () => {
    // The bug: save() derived the scheme from whatever value it was handed, whatever the key.
    // Choosing a one-minute refresh interval stored "60", which is not "dark", so the page was
    // repainted light underneath a dark MudBlazor theme — every component inverted against the
    // background until the next reload.
    const { theme, attributes, stored } = load();
    theme.applyResolved(true);

    theme.save('abom.follow', '60');

    assert.equal(attributes['data-theme'], 'dark', 'a refresh interval is not a color scheme');
    assert.equal(stored['abom.follow'], '60', 'and it still has to be saved');
});

test('saving graph colors leaves the scheme alone', () => {
    const { theme, attributes } = load();
    theme.applyResolved(true);

    theme.save('abom.kindColors', 'd:repo=#FF0000');

    assert.equal(attributes['data-theme'], 'dark');
});
