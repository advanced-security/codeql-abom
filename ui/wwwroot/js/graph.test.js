import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
    seed, repoOf, orgOf, edgeClass, groupOf, anchorKey, keyOf, faded,
    nodeRadius, isSelected, particleSpeed,
} from './graph.js';

// These are the pure decisions behind the whole view: which edges are drawn, how big a node is,
// what groups with what. Every bug this file has had lived in one of them, and none of it was
// reachable by any test until now.

test('an edge is classified by the closest true relationship', () => {
    const a = { key: 'octo/app/.github/workflows/ci.yml', repository: 'octo/app' };
    const b = { key: 'octo/app/.github/actions/build', repository: 'octo/app' };
    const c = { key: 'octo/other/.github/workflows/ci.yml', repository: 'octo/other' };
    const d = { key: 'actions/checkout' };

    assert.equal(edgeClass(a, b), 'sameRepo');
    // Same repository outranks same owner, or the broadest reading would always win.
    assert.equal(edgeClass(a, c), 'sameOwner');
    assert.equal(edgeClass(a, d), 'crossOwner');
});

test('an owner node compares by its account and not by a repository it does not have', () => {
    const org = { key: 'org:actions', kind: 'org', owner: 'actions' };
    const action = { key: 'actions/checkout' };
    const foreign = { key: 'docker/build-push-action' };

    assert.equal(orgOf(org), 'actions');
    assert.equal(repoOf(org), '', 'an account owns repositories rather than being one');
    assert.equal(edgeClass(org, action), 'sameOwner');
    assert.equal(edgeClass(org, foreign), 'crossOwner');
});

test('grouping by workflow does not put every node in a group of its own', () => {
    // The bug: with steps folded away nothing has a container, so each node became its own
    // group and collapsing changed nothing at all.
    const workflow = { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow' };
    const step = { key: 'step:1', kind: 'step', container: 'octo/app/.github/workflows/ci.yml' };
    const action = { key: 'actions/checkout', kind: 'action' };

    assert.equal(groupOf(workflow, 'workflow'), workflow.key);
    assert.equal(groupOf(step, 'workflow'), workflow.key, 'a step belongs to the file holding it');
    assert.equal(groupOf(action, 'workflow'), '',
        'an action is referenced by workflows rather than living in one');
});

test('grouping by owner and by repository agree with the node key', () => {
    const wf = { key: 'octo/app/.github/workflows/ci.yml', repository: 'octo/app', kind: 'workflow' };
    assert.equal(groupOf(wf, 'owner'), 'octo');
    assert.equal(groupOf(wf, 'repo'), 'octo/app');
    assert.equal(groupOf(wf, 'none'), '');
});

test('a group anchors on a node that exists, so grouping cannot fight the layout', () => {
    const wf = { key: 'octo/app/.github/workflows/ci.yml', repository: 'octo/app', kind: 'workflow' };
    assert.equal(anchorKey(wf, 'owner'), 'org:octo');
    assert.equal(anchorKey(wf, 'repo'), 'octo/app');
    assert.equal(anchorKey({ key: 'org:octo', kind: 'org', owner: 'octo' }, 'repo'), '');
});

test('size carries reach, and selecting a node barely changes it', () => {
    const hub = { key: 'actions/setup-python', kind: 'action', dependentRepos: 66 };
    const leaf = { key: 'octo/app/.github/workflows/ci.yml', kind: 'workflow', dependentRepos: 0 };

    assert.ok(nodeRadius(hub) > nodeRadius(leaf), 'reach must be visible as size');

    // Selecting used to multiply by 2.2, which filled the canvas and hid the neighborhood the
    // selection was meant to reveal.
    const selected = nodeRadius(hub, new Set([hub.key]));
    assert.ok(selected / nodeRadius(hub) < 1.5, `selected node grew ${selected / nodeRadius(hub)}x`);
});

test('an owner is sized as a container rather than by the reach of its largest member', () => {
    // Inheriting reach drew the account larger than the dependency inside it, counting one
    // member's reach twice on screen.
    const org = { key: 'org:actions', kind: 'org', dependentRepos: 202 };
    const action = { key: 'actions/checkout', kind: 'action', dependentRepos: 202 };
    assert.ok(nodeRadius(org) < nodeRadius(action));
});

test('selection accepts a set or a single key', () => {
    assert.equal(isSelected(new Set(['a']), 'a'), true);
    assert.equal(isSelected(new Set(['a']), 'b'), false);
    assert.equal(isSelected('a', 'a'), true);
    assert.equal(isSelected(null, 'a'), false);
});

test('a link endpoint reads the same before and after the renderer rewrites it', () => {
    // force-graph replaces the string with the node object once seeded; both forms occur.
    assert.equal(keyOf('actions/checkout'), 'actions/checkout');
    assert.equal(keyOf({ key: 'actions/checkout' }), 'actions/checkout');
});

test('fading an edge changes its alpha and keeps its hue', () => {
    assert.equal(faded('rgba(255,110,156,0.85)'), 'rgba(255,110,156,0.06)');
    assert.equal(faded('rgb(255,110,156)'), 'rgba(255,110,156,0.06)');
    assert.equal(faded('#ff6e9c'), '#ff6e9c', 'a hex colour has no alpha to drop');
});

test('layout and animation are deterministic for the same corpus', () => {
    // Positions are seeded by key so the same graph always lays out identically; drift would
    // destroy the spatial memory a reader builds up.
    assert.equal(seed('actions/checkout'), seed('actions/checkout'));
    assert.notEqual(seed('actions/checkout'), seed('actions/cache'));

    const link = { source: 'a', target: 'b' };
    assert.equal(particleSpeed(link), particleSpeed(link));
});

test('particles move slowly enough to read as drift rather than a pulse', () => {
    const speeds = [];
    for (let i = 0; i < 200; i++) {
        speeds.push(particleSpeed({ source: `n${i}`, target: 'actions/setup-python' }));
    }
    const slowest = Math.min(...speeds);
    const fastest = Math.max(...speeds);

    // A traverse under about four seconds at 60fps reads as a pulse, which is what the shared
    // speed used to produce.
    assert.ok(1 / (fastest * 60) > 4, `fastest traverse is ${(1 / (fastest * 60)).toFixed(1)}s`);
    // Distinct periods are what pull them out of lockstep; there is no phase offset available.
    assert.ok(fastest / slowest > 2, `speed spread is only ${(fastest / slowest).toFixed(1)}x`);
});
