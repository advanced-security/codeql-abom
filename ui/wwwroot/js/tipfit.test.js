import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// tipfit.js is a plain script, like theme.js: index.html loads it before Blazor exists and it
// publishes onto window. Evaluating the source is the only way to reach it from here.
function load(width) {
    const window = {};
    const document = { documentElement: { clientWidth: width } };
    const source = readFileSync(fileURLToPath(new URL('./tipfit.js', import.meta.url)), 'utf8');
    new Function('window', 'document', source)(window, document);
    return window.abomTipFit;
}

// What MudBlazor leaves: a box at `left`, moved only by whatever translate is on it now.
function tip(left, width) {
    return {
        left,
        style: { translate: '' },
        getBoundingClientRect() {
            const dx = parseFloat(this.style.translate) || 0;
            return { left: this.left + dx, right: this.left + width + dx, width };
        },
    };
}

test('a tip inside the window stays where it was placed', () => {
    const { shift } = load(390);
    assert.equal(shift({ left: 30, right: 350, width: 320 }, 390), 0);
});

test('a tip past either edge moves back by its overflow and a margin', () => {
    const { shift } = load(390);
    assert.equal(shift({ left: 200, right: 520, width: 320 }, 390), -138);
    assert.equal(shift({ left: -40, right: 280, width: 320 }, 390), 48);
});

test('a tip wider than the window starts at its left margin', () => {
    const { shift } = load(300);
    assert.equal(shift({ left: 40, right: 360, width: 320 }, 300), -32);
});

// The fit is read back off the element it moved, so running it again, as every mutation of the
// tip does, changes nothing; and a tip MudBlazor has since placed somewhere else is fitted from
// where it now is, not from where it was.
test('fitting a tip again changes nothing, and a tip placed again is fitted afresh', () => {
    const { fit } = load(390);
    const el = tip(200, 320);
    fit(el);
    assert.equal(el.style.translate, '-138px');
    assert.equal(el.getBoundingClientRect().right, 382);
    fit(el);
    assert.equal(el.style.translate, '-138px');

    el.left = 10;
    fit(el);
    assert.equal(el.style.translate, '');
    assert.equal(el.getBoundingClientRect().left, 10);
});
