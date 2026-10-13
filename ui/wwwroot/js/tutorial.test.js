import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

function load(storage = {}) {
    const window = {};
    const document = {};
    const source = readFileSync(new URL('./tutorial.js', import.meta.url), 'utf8');
    new Function('window', 'document', 'localStorage', source)(window, document, storage);
    return window.abomTutorial;
}

test('a spotlight is clipped to the viewport rather than drawing negative holes', () => {
    const t = load();
    assert.deepEqual(t.clip({ left: -10, top: 50, right: 210, bottom: 500 }, 200, 300),
        { x: 0, y: 44, width: 200, height: 256 });
    assert.equal(t.clip({ left: 10, top: 500, right: 90, bottom: 700 }, 200, 300), null);
});

test('the coach avoids the spotlight when there is room beside it', () => {
    const t = load();
    const box = { x: 20, y: 100, width: 500, height: 400 };
    const p = t.place(box, 360, 300, 1440, 900);
    assert.ok(p.x >= box.x + box.width + 12);
    assert.ok(p.y >= 8 && p.y + 300 <= 892);
});

test('a coach fits narrow and short viewports', () => {
    const t = load();
    const p = t.place({ x: 0, y: 80, width: 380, height: 240 }, 360, 300, 390, 600);
    assert.ok(p.x >= 8 && p.x + 360 <= 382);
    assert.ok(p.y >= 8 && p.y + 300 <= 592);
});

test('storage failures are delivered to the caller rather than silently losing completion', () => {
    const t = load({
        getItem() { throw new Error('storage denied'); },
        setItem() { throw new Error('quota exceeded'); },
    });
    assert.throws(() => t.load('tutorial'), /storage denied/);
    assert.throws(() => t.save('tutorial', '{}'), /quota exceeded/);
});

test('guided actions use the real control address and identity', () => {
    const t = load();
    const node = { getAttribute: name => name === 'href' ? '/node/acme/action?at=v2' : null };
    assert.deepEqual(t.activation(node, 'node'), { kind: 'node', href: '/node/acme/action?at=v2', value: '' });
    const finding = { getAttribute: name => name === 'data-abom-review-finding' ? '42' : null };
    assert.deepEqual(t.activation(finding, 'finding'), { kind: 'finding', href: '', value: '42' });
    const tab = { getAttribute: name => name === 'data-abom-unit' ? 'advisories' : null };
    assert.deepEqual(t.activation(tab, 'evidence-tab'), { kind: 'evidence-tab', href: '', value: 'advisories' });
});
