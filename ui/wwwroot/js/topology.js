// Which node belongs with which: ownership, repository, grouping and edge classification.
// Pure functions over node and link shapes, so every decision here is testable on its own.

/** Deterministic per-key seed, so the same corpus always lays out identically. */
export function seed(key) {
    let h = 2166136261;
    for (let i = 0; i < key.length; i++) {
        h ^= key.charCodeAt(i);
        h = Math.imul(h, 16777619);
    }
    return (h >>> 0) / 4294967295;
}

/** The repository a node belongs to. An external action carries it in its own key. */
export function repoOf(node) {
    if (node.kind === 'org') return '';
    if (node.repository) return node.repository;
    const parts = (node.key || '').split('/');
    return parts.length >= 2 ? `${parts[0]}/${parts[1]}` : (node.key || '');
}

export function orgOf(node) {
    // An account node names its owner directly; everything else carries it as a path segment.
    if (node.kind === 'org') return node.owner || (node.key || '').replace(/^org:/, '');
    return repoOf(node).split('/')[0];
}

/**
 * How far an edge reaches: within one repository, within one account, or across accounts.
 *
 * Same repository wins over same account, so the closest true relationship is the one reported
 * rather than the broadest. Nothing here is "external": the comparison is between the two
 * endpoints, and ABOM privileges no account of its own.
 */
export function edgeClass(s, t) {
    if (!s || !t) return 'crossOwner';
    if (repoOf(s) === repoOf(t)) return 'sameRepo';
    return orgOf(s) === orgOf(t) ? 'sameOwner' : 'crossOwner';
}

/** What a node is grouped under, derived from its own key so no extra payload is needed. */
export function groupOf(node, by) {
    switch (by) {
        case 'owner':
            return node.kind === 'org' ? (node.owner || '') : orgOf(node);
        case 'repo':
            return node.kind === 'org' ? '' : repoOf(node);
        case 'workflow':
            // A step or job belongs to the file holding it, and a workflow is its own group. An
            // action is *referenced* by workflows rather than living in one, so it has no
            // workflow to belong to and must not become a group of one.
            if (node.container) return node.container;
            return node.kind === 'workflow' || node.kind === 'reusable_workflow'
                ? node.key
                : '';
        default:
            return '';
    }
}

/** Everything the current grouping cannot place, named rather than scattered. */
export const UNGROUPED = '(not in a workflow)';

/** The existing node a member should gather around, if there is one. */
export function anchorKey(node, by) {
    switch (by) {
        case 'owner': {
            const owner = groupOf(node, 'owner');
            return owner ? `org:${owner}` : '';
        }
        case 'repo':
            return node.kind === 'org' ? '' : repoOf(node);
        case 'workflow':
            return node.container || '';
        default:
            return '';
    }
}

export function touches(link, key) {
    const s = link.source, t = link.target;
    return (s && (s.key || s) === key) || (t && (t.key || t) === key);
}

export function touchesAny(link, keys) {
    return keys.has(keyOf(link.source)) || keys.has(keyOf(link.target));
}

/** force-graph rewrites endpoints into node objects once seeded, so both forms have to work. */
export function keyOf(end) {
    return end && end.key !== undefined ? end.key : end;
}
