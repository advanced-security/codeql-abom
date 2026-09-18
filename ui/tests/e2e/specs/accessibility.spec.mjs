import { test, expect, ready, corpusSize, ROUTES } from '../harness.mjs';

// A control whose only label is a hover tooltip is unnamed for anybody not using a mouse. That is
// not a polish item on the Explorer, where the colour swatches, the kind checkboxes and the scope
// bar are most of the controls on screen: it is the surface being unusable from the keyboard.
//
// Measured from the DOM rather than from the markup, because the name a reader is given is
// whatever the browser computes — a `MudTooltip` renders as a hover popover and contributes
// nothing, while a `MudCheckBox` with a `Label` wraps its input in a `<label>` that does.
//
// bUnit cannot do this: it has no layout, so it cannot tell a visible control from one inside a
// closed popover, and a tooltip on a MudBlazor wrapper lands on a different element than the one
// that takes focus.

/**
 * What has to be on screen before a surface is worth measuring.
 *
 * `ready()` only says Blazor booted. Every surface then loads asynchronously, and measuring in
 * between reports an empty page as a clean one — which it did: Settings passed with its two
 * account checkboxes not yet rendered.
 */
const ANCHOR = {
    '/': '[data-abom-entry]',
    '/bom': '.mud-table-body .mud-table-row',
    '/explore': '.abom-keypanel',
    '/inventory': '.mud-table-body .mud-table-row',
    '/settings': '[data-abom-declared]',
};

/**
 * Controls that are not ABOM's to name, listed rather than skipped so the exception cannot grow.
 *
 * MudBlazor 8.15's `MudDataGridPager` gives its four navigation buttons an `aria-label` and its
 * page-size `MudSelect` none, and the component takes no parameter that reaches the inner input.
 * Every grid on every surface inherits it.
 *
 * `MudFocusTrap`, which every `MudDialog` renders, wraps its content in four empty focusable
 * sentinels that bounce keyboard focus back inside the dialog. They are focus plumbing rather than
 * controls, and naming them would have a reader announce something that is not there.
 */
const NOT_OURS = ['.mud-table-pagination-select', '.mud-focus-trap'];

/**
 * Names every visible, focusable control the browser would announce as nothing.
 *
 * Deliberately generous about what counts as a name — `title` and `placeholder` are both poor
 * labels and both counted, so a failure here is never arguable.
 */
const UNNAMED = (notOurs) => {
    const text = (el) => (el?.innerText ?? el?.textContent ?? '').replace(/\s+/g, ' ').trim();

    const labelText = (el) => {
        const parts = [];
        if (el.id) {
            for (const l of document.querySelectorAll(`label[for="${CSS.escape(el.id)}"]`)) {
                parts.push(text(l));
            }
        }
        // An implicit label wraps the control, so its own text has to come out of the comparison
        // or a button would be named by itself.
        const wrapper = el.closest('label');
        if (wrapper) parts.push(text(wrapper).replace(text(el), '').trim());
        return parts.join(' ').trim();
    };

    const named = (el) => {
        const by = el.getAttribute('aria-labelledby');
        if (by) {
            const from = by
                .split(/\s+/)
                .map((id) => text(document.getElementById(id)))
                .join(' ')
                .trim();
            if (from) return true;
        }
        return Boolean(
            el.getAttribute('aria-label')?.trim() ||
                text(el) ||
                el.getAttribute('title')?.trim() ||
                el.getAttribute('placeholder')?.trim() ||
                el.getAttribute('alt')?.trim() ||
                (el.tagName === 'INPUT' &&
                    el.value?.trim() &&
                    /^(submit|button|reset)$/i.test(el.type)) ||
                labelText(el),
        );
    };

    const focusable = [
        'a[href]',
        'button',
        'input:not([type=hidden])',
        'select',
        'textarea',
        '[role=button]',
        '[role=checkbox]',
        '[tabindex]:not([tabindex="-1"])',
    ].join(',');

    const out = [];
    for (const el of document.querySelectorAll(focusable)) {
        if (el.disabled) continue;
        if (el.closest('[aria-hidden="true"]')) continue;
        // A control inside a closed popover is not on screen, so it is not one a reader has met.
        if (!el.getClientRects().length) continue;
        if (notOurs.some((sel) => el.closest(sel))) continue;
        if (named(el)) continue;
        const where = String(el.className?.baseVal ?? el.className ?? '').trim();
        out.push(`${el.tagName.toLowerCase()}${el.type ? `[${el.type}]` : ''} .${where}`);
    }
    return out;
};

test.describe('every control says what it is', () => {
    for (const route of ROUTES) {
        test(`${route} offers no control the browser would announce as nothing`, async ({
            page,
            request,
            baseURL,
        }) => {
            const summary = await corpusSize(request, baseURL);
            test.skip(
                !summary.seedRepos,
                'an empty corpus renders empty states rather than the controls this measures',
            );

            await page.goto(route);
            await ready(page);
            await expect(page.locator(ANCHOR[route]).first()).toBeVisible({ timeout: 30_000 });

            // The property bar holds nothing until a key is chosen, so its controls are only
            // reachable — and only measurable — one click in.
            if (route === '/explore') {
                const key = page.locator('[data-abom-property-key]').first();
                if (await key.count()) {
                    await key.click();
                    await expect(page.locator('[data-abom-property-exclude]').first()).toBeVisible();
                }
            }

            const unnamed = await page.evaluate(UNNAMED, NOT_OURS);
            expect(unnamed, `${route}: ${unnamed.length} controls with no accessible name`).toEqual(
                [],
            );
        });
    }

    // The exception above is only safe while it stays one control on one component. Asserting that
    // it is still there keeps it from being quietly widened into a way of passing.
    test('the one control ABOM cannot name is the data grid page size', async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no corpus, so no grid to page');

        await page.goto('/inventory');
        await ready(page);
        await expect(page.locator(ANCHOR['/inventory']).first()).toBeVisible({ timeout: 30_000 });

        const unnamed = await page.evaluate(UNNAMED, []);
        expect(unnamed.length, 'unnamed controls once the exception is withdrawn').toBe(1);
        const only = await page.locator('.mud-table-pagination-select input').count();
        expect(only, 'and it is the pager select').toBeGreaterThan(0);
    });
});

test.describe('the scope bar is operable without a mouse', () => {
    // Naming a control is half of it. The exclude buttons sit inside a `MudButtonGroup` and carry
    // an icon and no text, so if they were ever made unreachable by Tab the names above would be
    // announced to nobody.
    test('every property control can be reached and used from the keyboard', async ({
        page,
        request,
        baseURL,
    }) => {
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.seedRepos, 'no corpus, so no properties to narrow by');

        await page.goto('/explore');
        await ready(page);
        await expect(page.locator('.abom-keypanel')).toBeVisible({ timeout: 30_000 });

        const key = page.locator('[data-abom-property-key]').first();
        test.skip((await key.count()) === 0, 'this corpus carries no custom repository property');
        await key.click();

        const exclude = page.locator('[data-abom-property-exclude]').first();
        await expect(exclude).toBeVisible();
        await exclude.focus();
        await expect(exclude).toBeFocused();
        await page.keyboard.press('Enter');

        // A term that was added is one the keyboard actually operated, rather than merely
        // reached.
        await expect(page.locator('[data-abom-property-drop]').first()).toBeVisible();
    });
});
