import { test } from 'node:test';
import assert from 'node:assert/strict';
import { revealRow } from './rows.js';

// A grid's scroll window, its pinned header, and one open row with its panel under it, with the
// geometry a browser reports.
function grid({ top = 100, bottom = 700, head = 40, rowTop, panelBottom, scrollTop = 0 }) {
    const container = {
        scrollTop,
        getBoundingClientRect: () => ({ top, bottom }),
        querySelector: (s) => (s === 'thead' ? { getBoundingClientRect: () => ({ height: head }) } : null),
    };
    const row = {
        getBoundingClientRect: () => ({ top: rowTop }),
        nextElementSibling: { getBoundingClientRect: () => ({ bottom: panelBottom }) },
        closest: (s) => (s === '.mud-table-container' ? container : null),
    };
    const root = { querySelector: (s) => (s === '[data-abom-open-row]' ? row : null) };
    return { container, root };
}

test('a row whose panel already fits is left exactly where it was clicked', () => {
    const { container, root } = grid({ rowTop: 300, panelBottom: 650, scrollTop: 80 });

    assert.equal(revealRow('[data-abom-open-row]', root), true);
    assert.equal(container.scrollTop, 80);
});

test('a panel running past the window scrolls up only far enough to show its end', () => {
    const { container, root } = grid({ rowTop: 500, panelBottom: 820 });

    revealRow('[data-abom-open-row]', root);
    assert.equal(container.scrollTop, 120);
});

test('a panel taller than the window stops with its row under the header, not above it', () => {
    const { container, root } = grid({ rowTop: 300, panelBottom: 1200 });

    revealRow('[data-abom-open-row]', root);
    // 200px below the window's top, 40 of them the header it must clear.
    assert.equal(container.scrollTop, 160);
});

test('a row scrolled above the window is brought back under the header', () => {
    const { container, root } = grid({ rowTop: 20, panelBottom: 300, scrollTop: 500 });

    revealRow('[data-abom-open-row]', root);
    assert.equal(container.scrollTop, 380);
});

test('a row that is not drawn moves nothing and says so', () => {
    const root = { querySelector: () => null };

    assert.equal(revealRow('[data-abom-open-row]', root), false);
});
