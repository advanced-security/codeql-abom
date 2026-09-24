import { test, expect, ready, on } from '../harness.mjs';

// The marks and a session are drawn only by a server given a query directory. `make e2e-corpus`
// serves the corpus pack, whose queries leave a mark in every state and whose first request was
// worked by a seeded session; every test here skips against a server without one.

async function served(request, baseURL, path) {
    const res = await request.get(`${baseURL}${path}`);
    expect(res.ok(), `the server answered ${path}`).toBeTruthy();
    return await res.json();
}

async function workingCopies(request, baseURL) {
    return (await served(request, baseURL, '/api/queries')).queries.filter((q) => q.working);
}

test.describe('authoring', () => {
    // A mark is served, never computed in the browser, so the page draws exactly the states the
    // server sent, each with a name a screen reader can say.
    test('a custom catalog row draws the marks it was served, each named', on('/codeql?tab=catalog'), async ({
        page, request, baseURL,
    }) => {
        const working = await workingCopies(request, baseURL);
        test.skip(working.length === 0, 'this server was given no query directory');

        await page.setViewportSize({ width: 1280, height: 900 });
        await page.goto('/codeql?tab=catalog');
        await ready(page);
        await expect(page.locator('[data-abom-queries-table] [data-abom-steps]')).toHaveCount(working.length);

        const drawn = await page.evaluate(() => [...document.querySelectorAll('[data-abom-steps]')].flatMap((strip) => {
            const id = strip.closest('tr').querySelector('[data-abom-query-id]').textContent.trim();
            return [...strip.querySelectorAll('[data-abom-step]')].map((mark) => ({
                mark: `${id} ${mark.getAttribute('data-abom-step')} ${mark.getAttribute('data-abom-step-state')}`,
                named: (mark.getAttribute('aria-label') ?? '').trim().length > 0,
            }));
        }));
        const want = working.flatMap((q) => q.steps.map((s) => `${q.queryId} ${s.step} ${s.state}`)).sort();
        expect(drawn.map((d) => d.mark).sort(), 'the marks drawn against the marks served').toEqual(want);
        expect(drawn.filter((d) => !d.named).map((d) => d.mark), 'marks with no name').toEqual([]);
        expect(new Set(want.map((m) => m.split(' ')[2])).size, 'states the corpus pack shows')
            .toBeGreaterThanOrEqual(3);
    });

    // Both directions of the link: a catalog row names the requests about it, and each opens the
    // request's own row.
    test('a catalog row opens the request that asked for its query', on('/codeql?tab=catalog', '/codeql?tab=requests'), async ({
        page, request, baseURL,
    }) => {
        const asked = (await workingCopies(request, baseURL)).find((q) => q.requests.length > 0);
        test.skip(!asked, 'no query in this server\'s directory was asked for');

        await page.goto(`/codeql?tab=catalog&query=${encodeURIComponent(asked.queryId)}`);
        await ready(page);
        const link = page.locator(`[data-abom-query-request="${asked.requests[0]}"]`);
        await expect(link).toBeVisible();
        await link.click();

        await expect(page).toHaveURL(new RegExp(`tab=requests&request=${asked.requests[0]}$`));
        await expect(page.locator(`[data-abom-request-open="${asked.requests[0]}"]`)).toBeVisible();
    });

    // A session needs no agent to be drawn, and what it was refused is the security record, so
    // it is read with the reason the agent was given, at the width the grid is held to.
    test('a worked request shows its steps, its session, what was refused and the tree printed', on('/codeql?tab=requests'), async ({
        page, request, baseURL,
    }) => {
        const requests = (await served(request, baseURL, '/api/query-requests')).requests;
        let worked;
        for (const r of requests) {
            if ((await served(request, baseURL, `/api/query-requests/${r.rowId}/sessions`)).sessions.length) {
                worked = r;
                break;
            }
        }
        test.skip(!worked, 'no request on this server has a session');

        await page.setViewportSize({ width: 1280, height: 900 });
        await page.goto(`/codeql?tab=requests&request=${worked.rowId}`);
        await ready(page);
        const open = page.locator(`[data-abom-request-open="${worked.rowId}"]`);
        await expect(open).toBeVisible();
        await expect(open.locator('[data-abom-request-step]')).toHaveCount(worked.steps.length);
        await expect(open.locator('[data-abom-session]')).toHaveCount(1);

        await open.locator('[data-abom-session-timeline]').click();
        await expect(open.locator('[data-abom-event]').filter({ hasText: 'not part of the authoring loop' }))
            .toHaveCount(1);
        await open.locator('[data-abom-ast-show]').first().click();
        await expect(open.locator('[data-abom-ast-body]')).toContainText('WorkflowImpl');

        const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        expect(over, 'the open request scrolls wider than the window').toBeLessThanOrEqual(0);
    });
});
