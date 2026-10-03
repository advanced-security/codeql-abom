import { test, expect, ready, on } from '../harness.mjs';

// From one row of Inventory › Actions to one version of one action, in a real browser: what the
// component tests cannot see is geometry, and whether a click inside a row's controls reaches the
// row. Both are only true or false where a browser lays the page out and dispatches the events.

/** The widest-reaching action, which on any populated corpus is used at several references. */
async function widest(request, baseURL) {
    const { items } = await (
        await request.get(`${baseURL}/api/actions?top=1&offset=0&sort=dependentRepos&desc=true`)
    ).json();
    return items[0];
}

// What the panel shows at once; the component's PanelRows.
const PANEL_ROWS = 6;

test('a row opens in place onto one page of its versions, and the actions below stay in view',
    on('/inventory'), async ({ page, request, baseURL }) => {
        const action = await widest(request, baseURL);
        test.skip(!action, 'no action on this corpus');

        await page.setViewportSize({ width: 1440, height: 900 });
        await page.goto('/inventory');
        await ready(page);
        const row = page.locator('tr', { has: page.locator(`[data-abom-action-expand="${action.nodeKey}"]`) });
        const before = (await row.boundingBox()).y;
        // A reader clicks a figure in the row, not the row element.
        await row.locator('[data-abom-level="repositories"]').click();

        const panel = page.locator(`[data-abom-action-panel="${action.nodeKey}"]`);
        await expect(panel).toBeVisible();
        await expect(panel.locator('[data-abom-version-ref]').first()).toBeVisible({ timeout: 15_000 });
        await expect(page).toHaveURL(/\/inventory/);
        expect(await panel.locator('[data-abom-version-ref]').count()).toBeLessThanOrEqual(PANEL_ROWS);
        if (action.distinctRefs > PANEL_ROWS) {
            await expect(panel.locator('button[aria-label="Next page"]')).toBeEnabled();
        }

        const geometry = await page.evaluate((key) => {
            const open = document.querySelector('.abom-actions-grid tr.abom-row-open');
            const container = open.closest('.mud-table-container');
            const panel = document.querySelector(`[data-abom-action-panel="${key}"]`);
            const head = panel.querySelector('thead').getBoundingClientRect();
            // :scope, or the outer list's tbody counts as this table's and its header row matches.
            const first = panel.querySelector(':scope tbody tr').getBoundingClientRect();
            const below = open.nextElementSibling.nextElementSibling;
            const box = (e) => e.getBoundingClientRect();
            return {
                rowTop: box(open).top, expander: box(open.querySelector('td')).right,
                panel: box(panel), headBottom: head.bottom, firstTop: first.top,
                belowTop: below ? box(below).top : null, container: box(container),
                tableWidth: box(open.closest('table')).width,
                rowColor: below && getComputedStyle(below.querySelector('td')).backgroundColor,
                panelColor: getComputedStyle(panel.closest('td')).backgroundColor,
                pageWidth: document.documentElement.scrollWidth, viewport: innerWidth,
            };
        }, action.nodeKey);
        expect(Math.abs(geometry.rowTop - before), 'the open row stays where it was clicked').toBeLessThanOrEqual(2);
        expect(geometry.panel.left, 'the panel is set in under the row').toBeGreaterThanOrEqual(geometry.expander - 1);
        expect(geometry.panel.width, 'and does not span the table, so no header above it reads as its own')
            .toBeLessThan(geometry.tableWidth * 0.9);
        expect(Math.abs(geometry.headBottom - geometry.firstTop), "the panel's own header sits on its rows")
            .toBeLessThanOrEqual(1);
        expect(geometry.panelColor, 'the panel is drawn apart from the rows').not.toBe(geometry.rowColor);
        expect(geometry.panel.bottom, 'the panel ends inside the grid window').toBeLessThanOrEqual(geometry.container.bottom + 1);
        expect(geometry.belowTop, 'the next action is still in view').toBeLessThan(geometry.container.bottom);
        expect(geometry.pageWidth, 'nothing overflows the page sideways').toBeLessThanOrEqual(geometry.viewport);

        await panel.press('Escape');
        await expect(panel).toHaveCount(0);
    });

test('a control inside a row keeps its own meaning and does not also open the row',
    on('/inventory'), async ({ page, request, baseURL }) => {
        const { items } = await (
            await request.get(`${baseURL}/api/actions?top=1&offset=0&flags=findings`)
        ).json();
        test.skip(!items?.length, 'no action carries a finding on this corpus');

        await page.goto('/inventory?flags=findings');
        await ready(page);
        await page.locator(`[data-abom-review-asset="${items[0].nodeKey}"]`).click();

        await expect(page.locator('[data-abom-asset-review]')).toBeVisible();
        await expect(page.locator('[data-abom-action-panel]')).toHaveCount(0);
    });

test('a reference opens the details page narrowed to it, and the page agrees with the API',
    on('/inventory', '/node'), async ({ page, request, baseURL }) => {
        const action = await widest(request, baseURL);
        test.skip(!action, 'no action on this corpus');
        const versions = await (await request.get(
            `${baseURL}/api/node/${action.nodeKey}/-/versions`)).json();
        const tag = versions.items.slice(0, PANEL_ROWS).find((v) => v.reference && !/^[0-9a-f]{40}$/.test(v.reference) && v.direct > 0);
        test.skip(!tag, 'no tag with a starting point naming it on this corpus');

        await page.goto('/inventory');
        await ready(page);
        await page.locator(`[data-abom-action-expand="${action.nodeKey}"]`).click();
        await page.locator(`[data-abom-action-panel="${action.nodeKey}"] [data-abom-version-ref="${tag.reference}"]`).click();

        await expect(page).toHaveURL(new RegExp(`/node/.*[?&]at=${encodeURIComponent(tag.reference)}`));
        await ready(page);
        const uses = await (await request.get(
            `${baseURL}/api/node/${action.nodeKey}/-/uses?at=${encodeURIComponent(tag.reference)}&top=25`)).json();
        await expect(page.locator('[data-abom-uses-said]')).toContainText(
            `of ${uses.total.toLocaleString('en-US')} use`);
        for (const reference of await page.locator('[data-abom-use-reference]').allTextContents()) {
            expect(reference.trim()).toBe(tag.reference);
        }
        await expect(page.locator('[data-abom-tag-change]')).toBeVisible();
        await expect(page.locator('.mud-typography-h6.abom-num')).toHaveCount(0);
    });
