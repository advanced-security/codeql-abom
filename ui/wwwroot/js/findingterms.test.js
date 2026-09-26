import { test } from 'node:test';
import assert from 'node:assert/strict';

import { answersFindingTerms, mergeInto, ruleMatches } from './graph.js';

// The finding terms are applied per finding, as the set path applies them, from the kinds of
// finding a node carries whole. A node holding a warning for one rule and an error for another
// must not answer `rules=A&severity=error`.

const node = {
    key: 'octo/app/.github/workflows/ci.yml',
    facets: [
        { rule: 'actions/unpinned-tag', provider: 'CodeQL', severity: 'warning', band: 'medium', count: 3 },
        { rule: 'actions/code-injection/critical', severity: 'error', securitySeverity: 9.3, band: 'critical', count: 1 },
    ],
};

test('a rule and a severity asked together match one finding that is both', () => {
    assert.equal(answersFindingTerms(node, { rules: ['actions/unpinned-tag'], severity: 'error' }), false);
    assert.equal(answersFindingTerms(node, { rules: ['actions/code-injection/critical'], severity: 'error' }), true);
});

test('a provider matches only a finding that recorded it, never an unattributed one', () => {
    assert.equal(answersFindingTerms(node, { provider: 'CodeQL', severity: 'error' }), false);
    assert.equal(answersFindingTerms(node, { provider: 'CodeQL' }), true);
    assert.equal(answersFindingTerms({ facets: [{ rule: 'r', severity: 'error', count: 1 }] },
        { provider: 'CodeQL' }), false);
});

test('a rule written as a prefix matches what the set path matches', () => {
    assert.equal(ruleMatches(['actions/*'], 'actions/unpinned-tag'), true);
    assert.equal(ruleMatches(['actions/code-injection/*'], 'actions/unpinned-tag'), false);
    assert.equal(ruleMatches(['actions/unpinned-tag'], 'actions/unpinned-tag-extra'), false);
    assert.equal(answersFindingTerms(node, { rules: ['actions/code-injection/*'] }), true);
});

test('an excluded rule drops the node carrying it, whatever else it carries', () => {
    assert.equal(answersFindingTerms(node, { notRules: ['actions/code-injection/*'] }), false);
    assert.equal(answersFindingTerms(node, { notRules: ['actions/other'] }), true);
    // Exclusion alone keeps what carries nothing, as the dependencies listing does.
    assert.equal(answersFindingTerms({ key: 'actions/checkout', facets: [] }, { notRules: ['actions/*'] }), true);
});

test('a node with no findings answers no finding question', () => {
    const clean = { key: 'actions/checkout', facets: [] };
    for (const f of [{ rules: ['actions/*'] }, { severity: 'error' }, { provider: 'CodeQL' },
        { bands: ['unknown'] }]) {
        assert.equal(answersFindingTerms(clean, f), false, JSON.stringify(f));
    }
    assert.equal(answersFindingTerms(clean, {}), true, 'asking nothing narrows nothing');
});

// A band is read from the facet the server banded, and beside a rule it names one finding that is
// both: this node's pinning warning is medium, so critical pinning is nothing it holds.
test('a band asked beside a rule matches one finding that is both', () => {
    assert.equal(answersFindingTerms(node, { bands: ['critical'] }), true);
    assert.equal(answersFindingTerms(node, { bands: ['high', 'low'] }), false);
    assert.equal(answersFindingTerms(node, { rules: ['actions/unpinned-tag'], bands: ['critical'] }), false);
    assert.equal(answersFindingTerms(node, { rules: ['actions/unpinned-tag'], bands: ['medium'] }), true);
});

// A finding arriving after the page loaded changes a drawn node's kinds of finding, not the node,
// so a live filter by severity has to read what the delta carried rather than what it arrived with.
test('a delta replaces what a drawn node holds, and a node that holds nothing now holds nothing', () => {
    const drawn = { key: 'w', findings: 0, facets: [], rules: [] };
    const cleared = { key: 'c', findings: 1, rules: ['r'], facets: [{ rule: 'r', band: 'low', count: 1 }] };
    const all = { nodes: [drawn, cleared], links: [] };

    mergeInto(all, { metrics: [
        { key: 'w', findings: 1, facets: [{ rule: 'actions/code-injection/critical', severity: 'error', band: 'critical', count: 1 }] },
        { key: 'c', findings: 0 },
    ] });

    assert.equal(answersFindingTerms(drawn, { bands: ['critical'] }), true);
    assert.deepEqual(drawn.rules, ['actions/code-injection/critical']);
    assert.deepEqual(cleared.facets, []);
    assert.deepEqual(cleared.rules, []);
    assert.deepEqual(all.rules, ['actions/code-injection/critical']);
});
