import { test, expect, ready, on } from '../harness.mjs';

async function read(request, path) {
    const response = await request.get(path);
    expect(response.ok(), path).toBeTruthy();
    return response.json();
}

test('audit totals, paging and full export agree with the API', on('/codeql?tab=audit'), async ({ page, request }) => {
    const first = await read(request, '/api/authoring-events?limit=1');
    await page.goto('/codeql?tab=audit&limit=1');
    await ready(page);
    await expect(page.locator('[data-abom-audit-count]')).toContainText(`of ${first.total} events`);
    await expect(page.locator('[data-abom-event]')).toHaveCount(first.items.length);
    if (first.hasMore) {
        const original = first.items[0].rowId;
        await page.locator('[data-abom-audit-next]').click();
        await expect(page.locator(`[data-abom-event="${original}"]`)).toHaveCount(0);
        await page.locator('[data-abom-audit-previous]').click();
        await expect(page.locator(`[data-abom-event="${original}"]`)).toBeVisible();
    }
    const href = await page.locator('[data-abom-audit-export]').getAttribute('href');
    const response = await request.get(href);
    expect(response.ok()).toBeTruthy();
    const lines = (await response.text()).trim().split('\n').map(JSON.parse);
    const events = lines.filter(e => e.type === 'event');
    expect(events.length).toBe(first.total);
    expect(new Set(events.map(e => e.event.rowId)).size).toBe(first.total);
    expect(lines.at(-1)).toMatchObject({ type: 'completion', complete: true, through: first.through });
    await page.locator('[data-abom-audit-reset]').click();
    await expect(page).toHaveURL(/\/codeql\?tab=audit$/);
    await page.goBack();
    await expect(page).toHaveURL(/limit=1/);
});

// The reader reported typing into the search and seeing nothing happen: a filter applies as it is
// typed or chosen, with no button to press, and a chip takes away that filter alone.
test('typing a search or choosing an outcome filters the log at once, and a chip removes it',
    on('/codeql?tab=audit'), async ({ page, request }) => {
        const all = await read(request, '/api/authoring-events?limit=500');
        const tool = all.items.find(e => e.tool)?.tool;
        test.skip(!tool, 'this database has no recorded tool call');
        const searched = await read(request, `/api/authoring-events?text=${encodeURIComponent(tool)}&limit=1`);
        const refused = await read(request, '/api/authoring-events?decision=refused&limit=1');

        await page.goto('/codeql?tab=audit');
        await ready(page);
        await expect(page.getByRole('button', { name: /^(apply|search)/i })).toHaveCount(0);
        await page.locator('[data-abom-audit-search] input').fill(tool);
        await expect(page).toHaveURL(new RegExp(`[?&]text=${encodeURIComponent(tool)}(&|$)`));
        await expect(page.locator('[data-abom-audit-count]')).toContainText(`of ${searched.total} events`);
        await expect(page.locator('[data-abom-audit-chip="text"]')).toContainText(tool);

        await page.locator('[data-abom-audit-filter="decision"]')
            .locator('xpath=ancestor-or-self::div[contains(concat(" ", @class, " "), " mud-select ")][1]').click();
        await page.locator('.mud-popover-open .mud-list-item').filter({ hasText: /^\s*Refused\s*$/ }).click();
        await expect(page).toHaveURL(/[?&]decision=refused(&|$)/);
        await expect(page).toHaveURL(new RegExp(`[?&]text=${encodeURIComponent(tool)}(&|$)`));

        await page.locator('[data-abom-audit-chip="text"] .mud-chip-close-button').click();
        await expect(page).not.toHaveURL(/[?&]text=/);
        await expect(page.locator('[data-abom-audit-count]')).toContainText(`of ${refused.total} events`);
        await expect(page.locator('[data-abom-audit-chip]')).toHaveCount(1);
    });

test('audit remains compact, dated and usable at ordinary and narrow widths',
    on('/codeql?tab=audit'), async ({ page, request }) => {
        const data = await read(request, '/api/authoring-events?limit=100');
        for (const width of [1440, 900]) {
            await page.setViewportSize({ width, height: 900 });
            await page.goto('/codeql?tab=audit');
            await ready(page);
            await expect(page.locator('[data-abom-audit-count]')).toContainText(`of ${data.total} events`);
            await expect(page.locator('[data-abom-audit-pager]')).toHaveCount(2);
            if (data.items.length) {
                await expect(page.locator('[data-abom-event]').first().locator('time')).toHaveAttribute('datetime', data.items[0].at);
                await expect(page.locator('[data-abom-event]').first().locator(':scope > summary')).not.toContainText('duration not measured');
            }
            expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);
            expect(await page.locator('.abom-audit-list').evaluate(el => el.clientHeight)).toBeLessThanOrEqual(500);
            await expect(page.locator('[data-abom-audit-pager="bottom"]')).toBeVisible();
        }
    });

test('a rejected date explains how to correct the filter',
    on('/codeql?tab=audit'), async ({ page }) => {
        await page.goto('/codeql?tab=audit&from=bad');
        await ready(page);
        await expect(page.locator('[data-abom-audit-error]')).toContainText('RFC3339');
        await expect(page.locator('[data-abom-audit-error]')).toContainText('Correct the audit filters');
    });

test('a recorded session has direct request, event and reproduction entry points',
    on('/codeql?tab=requests', '/codeql?tab=audit'), async ({ page, request }) => {
        const snapshot = await read(request, '/api/authoring-events?limit=500');
        const event = snapshot.items.find(e => e.requestId > 0);
        test.skip(!event, 'this database has no recorded authoring session');
        await page.goto(`/codeql?tab=requests&request=${event.requestId}`);
        await ready(page);
        await page.locator(`[data-abom-request-audit][href$="request=${event.requestId}"]`).click();
        await expect(page).toHaveURL(new RegExp(`tab=audit&request=${event.requestId}$`));
        await expect(page.locator(`[data-abom-related-sessions] [data-abom-session="${event.sessionId}"]`)).toBeVisible();
        await expect(page.locator(`[data-abom-related-sessions] [data-abom-session="${event.sessionId}"] [data-abom-session-duration]`)).toBeVisible();
        await page.goto(`/codeql?tab=audit&session=${event.sessionId}&event=${event.rowId}`);
        await ready(page);
        await expect(page.locator(`[data-abom-event="${event.rowId}"]`)).toHaveAttribute('open', '');
        await page.getByText('TDD and reproduction evidence', { exact: true }).click();
        await expect(page.getByText('Evidence, not replay:', { exact: false })).toBeVisible();
        const response = await request.get(`/api/authoring-sessions/${event.sessionId}/reproduction?download=1`);
        expect(response.ok()).toBeTruthy();
        expect(await response.text()).toContain('<!-- audit-complete:');
        expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
    });

test('runs link to the scoped audit without inventing an authoring relation',
    on('/codeql?tab=runs', '/codeql?tab=audit'), async ({ page, request }) => {
        const runs = await read(request, '/api/runs');
        test.skip(runs.runs.length === 0, 'this database has no analysis run');
        await page.goto('/codeql?tab=runs');
        await ready(page);
        await page.locator('[data-abom-run-audit]').first().click();
        await expect(page).toHaveURL(/tab=audit&run=\d+/);
        await expect(page.locator('[data-abom-audit-count]')).toBeVisible();

        // A search wider than the run is offered under the sessions it widens, not apart from them.
        await page.goto(`/codeql?tab=runs&run=${runs.runs[0].rowId}`);
        await ready(page);
        const wider = page.locator('[data-abom-related-sessions] [data-abom-run-query-audit]');
        await expect(wider.first()).toBeVisible();
        expect(await wider.first().getAttribute('href')).toMatch(/tab=audit&query=/);
    });
