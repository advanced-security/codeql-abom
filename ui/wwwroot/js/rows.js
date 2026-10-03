// Row geometry the grid cannot do for itself.

/**
 * Scrolls a grid as little as it takes to show an opened row and the panel drawn under it, so the
 * rows around it stay where the reader left them. A panel taller than the window stops with its
 * row directly under the pinned header: what was clicked stays in view either way.
 *
 * @returns whether a row was found to show.
 */
export function revealRow(selector, root = globalThis.document) {
    const row = root.querySelector(selector);
    const container = row?.closest('.mud-table-container');
    if (!container) return false;
    const window = container.getBoundingClientRect();
    const head = container.querySelector('thead')?.getBoundingClientRect().height ?? 0;
    const toRow = row.getBoundingClientRect().top - window.top - head;
    const toEnd = (row.nextElementSibling ?? row).getBoundingClientRect().bottom - window.bottom;
    container.scrollTop += toRow < 0 ? toRow : Math.max(0, Math.min(toRow, toEnd));
    return true;
}
