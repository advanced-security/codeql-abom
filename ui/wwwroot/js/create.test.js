import { test } from 'node:test';
import assert from 'node:assert/strict';

// Everything in graph.test.js is a pure function, which left the one entry point the page
// actually calls untested: a stray edit put an undefined variable in create() and every check in
// `make verify` passed while the Explorer rendered nothing but an error bar. A fake renderer is
// enough to catch that class of mistake, and to hold the escape wiring in place.

function install() {
    const documentListeners = [];
    const windowListeners = [];
    const elementListeners = [];
    const registered = {};

    const graph = new Proxy({}, {
        get(_target, prop) {
            if (prop === 'controls') return () => ({});
            return (...args) => {
                registered[prop] = args;
                return graph;
            };
        },
    });

    const element = {
        isConnected: true,
        style: {},
        getBoundingClientRect: () => ({ top: 0, left: 0, width: 800, height: 600 }),
        appendChild() { },
        set innerHTML(_v) { },
        // The renderer writes its own control hint into the host, and the view rebinds gestures
        // on it, so the stub has to answer both the way a real element does.
        querySelector: () => null,
        addEventListener: (...a) => elementListeners.push(a),
        removeEventListener: (...a) => {
            const i = elementListeners.findIndex(([t, f]) => t === a[0] && f === a[1]);
            if (i >= 0) elementListeners.splice(i, 1);
        },
    };

    globalThis.document = {
        getElementById: () => element,
        createElement: () => ({ isConnected: true, style: {}, getContext: () => null }),
        querySelector: () => null,
        addEventListener: (...a) => documentListeners.push(a),
        removeEventListener: (...a) => {
            const i = documentListeners.findIndex(([t, f]) => t === a[0] && f === a[1]);
            if (i >= 0) documentListeners.splice(i, 1);
        },
    };
    globalThis.window = {
        ForceGraph3D: () => () => graph,
        scrollY: 0,
        innerHeight: 900,
        devicePixelRatio: 1,
        addEventListener: (...a) => windowListeners.push(a),
        removeEventListener: () => { },
    };
    // Never invokes the callback: drawLabels re-arms itself on every frame, and a stub that ran
    // it would recurse until the stack gave out.
    globalThis.requestAnimationFrame = () => 0;
    globalThis.ResizeObserver = class {
        // Takes the callback the real API takes, and never runs it: the graph re-arms itself on
        // every frame, so a stub that fired would recurse. A no-argument stub instead makes every
        // caller look like it passes one argument too many.
        constructor(callback) { this.callback = callback; }
        observe() { }
        disconnect() { }
    };
    globalThis.performance = { now: () => 0 };

    return { documentListeners, windowListeners, elementListeners, registered };
}

function fakeDotNet() {
    const calls = [];
    return { calls, invokeMethodAsync: (name, ...args) => { calls.push([name, ...args]); } };
}

const env = install();
const { create, dispose } = await import('./graph.js');

test('the graph can actually be created', () => {
    assert.equal(create('abom-graph', true, fakeDotNet()), true);
});

// Driven through the listeners the view binds itself, because the renderer's own click callbacks
// are gone: a node is drawn at half a pixel, so its hit test answered "empty space" for gestures
// aimed straight at one.
test('a click on empty canvas asks the page to drop the selection', () => {
    const net = fakeDotNet();
    create('abom-graph', true, net);
    const fire = (type, event) => {
        for (const [t, f] of env.elementListeners) if (t === type) f(event);
    };

    fire('pointerdown', { clientX: 10, clientY: 10 });
    fire('click', { clientX: 10, clientY: 10 });

    assert.deepEqual(net.calls, [['OnGraphCleared']]);
});

// A camera drag that happens to end over empty space is not the reader saying "never mind".
test('a drag that ends on empty canvas is not a click', () => {
    const net = fakeDotNet();
    create('abom-graph', true, net);
    const fire = (type, event) => {
        for (const [t, f] of env.elementListeners) if (t === type) f(event);
    };

    fire('pointerdown', { clientX: 10, clientY: 10 });
    fire('click', { clientX: 120, clientY: 90 });

    assert.deepEqual(net.calls, []);
});

test('escape asks the page to drop the selection', () => {
    const net = fakeDotNet();
    create('abom-graph', true, net);

    const keydown = env.documentListeners.find(([type]) => type === 'keydown')[1];
    keydown({ key: 'Escape', target: { tagName: 'CANVAS' } });

    assert.deepEqual(net.calls, [['OnGraphCleared']]);
});

test('escape in a text field is left to the field', () => {
    const net = fakeDotNet();
    create('abom-graph', true, net);

    const keydown = env.documentListeners.find(([type]) => type === 'keydown')[1];
    keydown({ key: 'Escape', target: { tagName: 'INPUT' } });

    assert.deepEqual(net.calls, []);
});

// The listener is on the document, so one left behind would answer for a page that is gone —
// and on a view the reader has already navigated away from, that reads as a phantom.
test('leaving the view takes its key listener with it', () => {
    create('abom-graph', true, fakeDotNet());
    const before = env.documentListeners.filter(([type]) => type === 'keydown').length;

    dispose('abom-graph');

    const after = env.documentListeners.filter(([type]) => type === 'keydown').length;
    assert.equal(before - after, 1);
});

// The gesture listeners exist because a rested view stops polling the camera, so they are bound
// for the lifetime of the view rather than per frame. Left behind, every rebuild adds another
// that wakes an instance no longer on screen.
test('leaving the view takes its gesture listeners with it', () => {
    create('abom-graph', true, fakeDotNet());
    const count = type => env.elementListeners.filter(([t]) => t === type).length;
    const before = {
        pointerdown: count('pointerdown'), wheel: count('wheel'), dblclick: count('dblclick'),
        pointermove: count('pointermove'), pointerleave: count('pointerleave'),
        // Owned here rather than left to the renderer, so they are ours to clean up too.
        pointerup: count('pointerup'), click: count('click'), contextmenu: count('contextmenu'),
    };

    dispose('abom-graph');

    for (const [type, had] of Object.entries(before)) {
        assert.ok(had > 0, `create() never bound ${type}`);
        assert.equal(count(type), had - 1, `${type} survived dispose`);
    }
});
