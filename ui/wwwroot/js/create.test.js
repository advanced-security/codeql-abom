import { test } from 'node:test';
import assert from 'node:assert/strict';

// Everything in graph.test.js is a pure function, which left the one entry point the page
// actually calls untested: a stray edit put an undefined variable in create() and every check in
// `make verify` passed while the Explorer rendered nothing but an error bar. A fake renderer is
// enough to catch that class of mistake, and to hold the escape wiring in place.

function install() {
    const documentListeners = [];
    const windowListeners = [];
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

    return { documentListeners, windowListeners, registered };
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

test('a click on empty canvas asks the page to drop the selection', () => {
    const net = fakeDotNet();
    create('abom-graph', true, net);

    const onBackground = env.registered.onBackgroundClick[0];
    onBackground();

    assert.deepEqual(net.calls, [['OnGraphCleared']]);
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
