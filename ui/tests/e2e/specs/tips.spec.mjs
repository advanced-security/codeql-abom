import { test, expect, ready, on, UNITS, EXPLORE_DRAWN } from '../harness.mjs';

// Every column header says what its values are and every action or link what it does, as the
// page actually renders. `check-style` reads the markup; this reads what a browser draws,
// including controls a component builds for itself and markup a suppression block hides.

const CODEQL_TABS = ['requests', 'catalog', 'runs', 'suites'];
const PAGES = ['/', ...CODEQL_TABS.map((t) => `/codeql?tab=${t}`), ...UNITS.map((u) => `/inventory?unit=${u}`),
    '/explore', EXPLORE_DRAWN, '/settings'];

// Controls MudBlazor draws for itself — pagers, sort arrows, pickers — which carry their own words.
const FRAMEWORK = ['.mud-table-pagination', '.sort-direction-icon', '.column-options', '.mud-input-adornment',
    '.mud-select', '.mud-picker', '.mud-snackbar', '.mud-overlay', '.mud-expand-panel-header', '.mud-popover'];

async function untipped(page) {
    return page.evaluate((framework) => {
        const drawn = (e) => { const b = e.getBoundingClientRect(); return b.width > 0 && b.height > 0; };
        const label = (e) => (e.getAttribute('aria-label') || e.textContent || e.className).trim().slice(0, 60);
        // Inside SVG the hover tip is a `<title>` child; a browser draws no tip from a title attribute there.
        const svgTip = (e) => e instanceof SVGElement && !!e.querySelector(':scope > title')?.textContent.trim();
        // A grid draws its empty-state message in a body cell shaped like a header; only thead names columns.
        const headers = [...document.querySelectorAll('thead th')]
            .filter((th) => drawn(th) && th.textContent.trim() && !th.querySelector('[data-abom-header-tip]'))
            .map(label);
        const actions = [...document.querySelectorAll('a[href], button, [role="button"]')]
            .filter((e) => drawn(e) && !framework.some((s) => e.closest(s))
                && !e.closest('[title]') && !e.closest('.mud-tooltip-root') && !svgTip(e))
            .map(label);
        return { headers, actions };
    }, FRAMEWORK);
}

for (const path of PAGES) {
    test(`${path} says what every header holds and every action does`, on(path), async ({ page }) => {
        await page.goto(path);
        await ready(page);

        const missing = await untipped(page);
        expect(missing.headers, 'headers without a tip').toEqual([]);
        expect(missing.actions, 'actions and links without a tip').toEqual([]);
    });
}

// A tip present in the markup is no use unless hovering shows it, at once and in this browser.
const CODEQL_TIPS = [
    ['[data-abom-tab="requests"]', 'Ask for a new CodeQL query, or a fix to one, from examples.'],
    ['[data-abom-tab="catalog"]', 'Every query version ABOM holds, its stage, and where it ran.'],
    ['[data-abom-tab="runs"]', 'Query runs over a scope, and what each found.'],
    ['[data-abom-tab="suites"]', 'Choose which query suites each crawl tier runs.'],
];

test('the CodeQL tabs and New request show what each is for on hover', on('/codeql'), async ({ page }) => {
    await page.goto('/codeql?tab=requests');
    await ready(page);

    for (const [selector, text] of CODEQL_TIPS) {
        await page.locator(selector).hover();
        await expect(page.locator('.mud-tooltip', { hasText: text })).toBeVisible();
    }
    // Its words depend on whether somebody is signed in, so what is drawn is held to what is declared.
    // The tip's span is hovered: a disabled button takes no pointer, and the span is what shows why.
    const request = page.locator('[data-abom-tip]', { has: page.locator('[data-abom-request-new]') });
    const declared = await request.getAttribute('data-abom-tip');
    await request.hover();
    await expect(page.locator('.mud-tooltip', { hasText: declared })).toBeVisible();
});

test('an action\'s details page says the same', on('/node'), async ({ page, request, baseURL }) => {
    const { items } = await (await request.get(`${baseURL}/api/actions?top=1&offset=0`)).json();
    test.skip(!items?.length, 'no action on this corpus');

    await page.goto(`/node/${encodeURIComponent(items[0].nodeKey)}`);
    await ready(page);
    await expect(page.locator('[data-abom-versions-summary]')).toBeVisible({ timeout: 15_000 });

    const missing = await untipped(page);
    expect(missing.headers, 'headers without a tip').toEqual([]);
    expect(missing.actions, 'actions and links without a tip').toEqual([]);
});
