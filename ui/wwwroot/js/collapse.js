// Folding each group into a single node, and the merged edges between groups.

import { groupOf, edgeClass, ungroupedName, keyOf } from './topology.js';

/** Builds one node per group, merging the edges between them. */
export function collapseGroups(it, nodes, links) {
    const by = it.groupBy && it.groupBy !== 'none' ? it.groupBy : 'owner';
    const groups = new Map();
    const members = new Map();
    const ungrouped = [];

    for (const n of nodes) {
        const membership = groupOf(n, by);
        if (!membership && (by === 'owner' || by === 'repo')) {
            n.groupKey = n.key;
            members.set(n.key, n);
            ungrouped.push(n);
            continue;
        }
        const name = membership || ungroupedName(by);
        let g = groups.get(name);
        if (!g) {
            g = {
                key: `group:${name}`, kind: 'org', path: name, ...identityOf(name, by),
                members: 0, findings: 0, dependentRepos: 0, rules: [], advisories: 0,
                // Both start at the strongest claim and are weakened by any member that cannot
                // support it, because a fold must never answer a question on behalf of a member
                // nobody has measured or analyzed.
                analyzed: true, reachKnown: true,
                // Summed rather than reduced to a boolean: a group of five dependencies with one
                // reference each analyzed out of twenty is not "analyzed", and it is not
                // "unanalyzed" either.
                refsInUse: 0, refsAnalyzed: 0,
                isExternal: true, refStyles: new Set(), edgeClasses: new Set(),
                // Seeded from the members' own center so a collapsed view sits where the
                // expanded one did.
                x: 0, y: 0, z: 0,
            };
            groups.set(name, g);
        }
        g.members++;
        g.findings += n.findings || 0;
        g.dependentRepos = Math.max(g.dependentRepos, n.dependentRepos || 0);
        // An unmeasured member contributes 0 to that maximum, so the total is a lower bound and
        // the group has to say so rather than presenting it as measured.
        if (n.reachKnown === false) g.reachKnown = false;
        if (n.analyzed === false) g.analyzed = false;
        g.refsInUse += n.refsInUse || 0;
        g.refsAnalyzed += n.refsAnalyzed || 0;
        if (!n.isExternal) g.isExternal = false;
        if (n.x !== undefined) { g.x += n.x; g.y += n.y; g.z += n.z || 0; }
        n.groupKey = g.key;
        members.set(n.key, g);
    }
    for (const g of groups.values()) {
        g.x /= g.members; g.y /= g.members; g.z /= g.members;
    }

    const merged = new Map();
    for (const e of links) {
        if (!e.shown) continue;
        const sourceKey = keyOf(e.source), targetKey = keyOf(e.target);
        const s = members.get(sourceKey);
        const t = members.get(targetKey);
        if (!s || !t) {
            throw new Error(`Cannot fold shown link ${sourceKey} -> ${targetKey}: missing endpoint node`);
        }
        if (s === t) continue;
        const id = `${s.key}>${t.key}`;
        const existing = merged.get(id);
        if (existing) { existing.count += e.count || 1; continue; }
        // A merged edge is a relationship in its own right and may not be the class any of its
        // members were: two same-owner edges can merge into one that crosses accounts. It has to
        // be judged as what it is, or a hidden class reappears once the graph is folded.
        const cls = edgeClass(s, t);
        merged.set(id, {
            source: s.key, target: t.key, kind: 'group', refs: [], count: e.count || 1,
            cls, shown: !it.edgeClasses || it.edgeClasses.has(cls),
        });
    }
    return { nodes: [...groups.values(), ...ungrouped], links: [...merged.values()] };
}

/**
 * What a group *is*, so that edges between groups classify by the real relationship.
 *
 * A group folded by repository still sits inside an account, and two of them under one account
 * are a same-owner edge rather than a cross-owner one. Without this a collapsed graph reported
 * every relationship as the same kind.
 */
function identityOf(name, by) {
    if (name === ungroupedName(by)) return { owner: '' };
    switch (by) {
        case 'repo':
            return { repository: name, owner: name.split('/')[0] };
        case 'grouping':
            // A grouping divides one organization; it is not an account and has no repository of
            // its own. Left blank so two groups read as crossing rather than as sharing an account
            // neither of them is.
            return { owner: '' };
        case 'workflow':
            return {
                repository: name.split('/').slice(0, 2).join('/'),
                owner: name.split('/')[0],
            };
        default:
            return { owner: name };
    }
}
