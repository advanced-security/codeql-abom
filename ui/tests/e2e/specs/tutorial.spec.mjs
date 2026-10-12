import { test, expect, ready, on } from '../harness.mjs';

test.use({ tutorialFresh: true });

async function start(page, lesson) {
    await page.goto('/');
    await ready(page);
    await page.locator('[data-abom-tutorial-ack]').click();
    await page.locator(`[data-abom-tutorial-start="${lesson}"]`).click();
    await expect(page.locator('[data-abom-tutorial-coach]')).toBeVisible();
}

async function settled(page) {
    await expect(page.locator('[data-abom-tutorial-coach]')).not.toHaveAttribute('data-abom-tutorial-ready', 'loading');
}

test('the overview follows Settings, coverage, CodeQL, then Inventory', on('/', '/settings', '/inventory', '/codeql'), async ({ page }) => {
    await start(page, 'orientation');
    const expected = [
        '/settings', '/inventory?unit=coverage', '/codeql?tab=requests', '/codeql?tab=catalog',
        '/codeql?tab=runs', '/codeql?tab=suites', '/inventory', '/inventory?unit=dependencies',
        '/node/', '/inventory?unit=workflows',
    ];
    const progress = page.locator('[data-abom-tutorial-progress]');
    for (let i = 0; i < expected.length; i++) {
        await settled(page);
        await expect(progress).toHaveAttribute('aria-valuemax', '10');
        await expect(progress).toHaveAttribute('aria-valuenow', String(i));
        if (i === 8) {
            // An empty dataset keeps this topic in the fixed count without inventing an Action.
            if (!await page.locator('[data-abom-tutorial-missing]').count())
                expect(new URL(page.url()).pathname).toMatch(/^\/node\//);
        } else {
            await expect(page).toHaveURL(new RegExp(expected[i].replace(/[?]/g, '\\?') + '$'));
        }
        const next = page.locator('[data-abom-tutorial-next]');
        if (i + 1 === expected.length) break;
        if (await next.isEnabled()) await next.click();
        else await page.locator('[data-abom-tutorial-active-action]').first().click();
        await expect(progress).toHaveAttribute('aria-valuenow', String(i + 1));
    }
    await expect(page.locator('[data-abom-tutorial-related]')).toHaveCount(2);
    await page.locator('[data-abom-tutorial-related="lifecycle"]').click();
    await expect(page.locator('.abom-tutorial-position').first()).toContainText('Understand the QLaaS lifecycle');
    await expect(progress).toHaveAttribute('aria-valuenow', '0');
    await page.keyboard.press('Escape');
    await expect(page.locator('[data-abom-tutorial-start="orientation"]')).toHaveAttribute('aria-label', /^Restart:/);
});

for (const [lesson, branch] of [
    ['orientation', ''], ['lifecycle', ''], ['new-rule', ''], ['improve-rule', ''],
    ['blast-radius', 'advisories'], ['blast-radius', 'findings'], ['action-dependents', ''],
]) {
    test(`guided ${lesson} ${branch} keeps its advertised count and changes no data`,
        on('/', '/settings', '/codeql', '/inventory'), async ({ page }) => {
            test.setTimeout(120_000);
            await start(page, lesson);
            const writes = [];
            page.on('request', request => {
                if (request.url().includes('/api/') && !['GET', 'HEAD'].includes(request.method()))
                    writes.push(`${request.method()} ${request.url()}`);
            });
            const progress = page.locator('[data-abom-tutorial-progress]');
            const total = Number(await progress.getAttribute('aria-valuemax'));
            for (let i = 0; i < total; i++) {
                await settled(page);
                await expect(progress).toHaveAttribute('aria-valuemax', String(total));
                await expect(progress).toHaveAttribute('aria-valuenow', String(i));
                await expect(page.locator('[data-abom-tutorial-coach] input, [data-abom-tutorial-coach] select')).toHaveCount(0);
                await expect(page.locator('[data-abom-tutorial-explain]')).toHaveCount(0);
                const copy = await page.locator('[data-abom-tutorial-coach]').innerText();
                expect(copy).not.toMatch(/\bseed\b|upstream provenance|Read explanation instead/i);
                if (i + 1 === total) {
                    await page.locator('[data-abom-tutorial-finish]').click();
                } else {
                    const next = page.locator('[data-abom-tutorial-next]');
                    if (await next.isEnabled()) await next.click();
                    else if (branch && i === 0)
                        await page.locator(`[data-abom-unit="${branch}"][data-abom-tutorial-active-action]`).click();
                    else await page.locator('[data-abom-tutorial-active-action]').first().click();
                    await expect(progress).toHaveAttribute('aria-valuenow', String(i + 1));
                }
            }
            await expect(page.locator('[data-abom-tutorial-coach]')).toHaveCount(0);
            expect(writes).toEqual([]);
            await expect(page.locator(`[data-abom-tutorial-start="${lesson}"]`)).toHaveAttribute('aria-label', /^Restart:/);
            await page.reload();
            await ready(page);
            await expect(page.locator('[data-abom-tutorial-welcome]')).toHaveCount(0);
            await page.locator(`[data-abom-tutorial-start="${lesson}"]`).click();
            await expect(progress).toHaveAttribute('aria-valuenow', '0');
            await page.keyboard.press('Escape');
        });
}

test('the highlighted Action is the real first link and only that link is allowed', on('/', '/inventory'), async ({ page }) => {
    await start(page, 'action-dependents');
    await settled(page);
    const first = page.locator('[data-abom-tutorial-action] a').first();
    if (await first.count()) {
        const href = await first.getAttribute('href');
        await expect(first).toHaveAttribute('data-abom-tutorial-active-action', '');
        await expect(first).not.toHaveAttribute('aria-disabled', 'true');
        await expect(page.locator('[data-abom-tutorial-next]')).toBeDisabled();
        await first.focus();
        await page.keyboard.press('Enter');
        await expect(page).toHaveURL(new URL(href, page.url()).href);
        await expect(page.locator('[data-abom-tutorial-progress]')).toHaveAttribute('aria-valuenow', '1');
    } else {
        await expect(page.locator('[data-abom-tutorial-missing]')).toBeVisible();
        await expect(page.locator('[data-abom-tutorial-next]')).toBeEnabled();
    }
    await page.keyboard.press('Escape');
    await expect(page.locator('[data-abom-tutorial-coach]')).toHaveCount(0);
});

test('missing data never adds hidden steps or requires another explanation choice', on('/', '/inventory'), async ({ page }) => {
    await start(page, 'action-dependents');
    await settled(page);
    await page.locator('[data-abom-tutorial-action] a').evaluateAll(links => links.forEach(link => link.remove()));
    await expect(page.locator('[data-abom-tutorial-missing]')).toBeVisible();
    await expect(page.locator('[data-abom-tutorial-next]')).toBeEnabled();
    await expect(page.locator('[data-abom-tutorial-progress]')).toHaveAttribute('aria-valuemax', '5');
    await page.setViewportSize({ width: 390, height: 740 });
    await expect.poll(async () => {
        const box = await page.locator('[data-abom-tutorial-coach]').boundingBox();
        return box.x >= 0 && box.x + box.width <= await page.evaluate(() => innerWidth);
    }).toBeTruthy();
    await page.locator('[data-abom-tutorial-exit]').click();
});
