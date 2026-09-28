/**
 * The WebGL dependency graph: lifecycle, filtering and selection.
 *
 * The decisions this file used to make itself now live beside their own tests: what belongs
 * with what in topology.js, how it looks in appearance.js, where it settles in forces.js.
 *
 * Switching view changes only the target radius, never the angle, so nodes travel along a ray
 * and stay trackable. That is what makes a switch read as re-orientation rather than reshuffle.
 */

import { instances } from './state.js';
import {
    seed, repoOf, orgOf, edgeClass, boundaryClass, groupOf, anchorKey, keyOf,
    ungroupedName,
} from './topology.js';
import {
    nodeColor, edgeColor, faded, withAlpha, nodeRadius, isSelected, buildNodeObject, shapesUsable,
    discoveryColor, coverageColor,
    internalMaterialClass, SHA,
} from './appearance.js';
import { applyForces } from './forces.js';
import { collapseGroups } from './collapse.js';
import {
    regionMembers, hullOf, inflate, hullArea, groupSizes, containsPoint, regionPadding, boxHull,
} from './regions.js';
import { particleCount, particleSpeed, startParticleCycle } from './particles.js';
import {
    fitPositions, clusterBoxes, applyPositions, releaseAll, layoutSummary, refusalText,
} from './serverlayout.js';

// Re-exported so the pure decisions can be imported by name from one place, including by tests.
export {
    seed, repoOf, orgOf, edgeClass, boundaryClass, groupOf, anchorKey, keyOf,
    faded, nodeRadius, isSelected, particleSpeed, collapseGroups, ungroupedName,
    discoveryColor, coverageColor,
};
export { gravityForce, spreadFor } from './forces.js';

// WebGL rendering for the ABOM dependency graph.
//
// One renderer, two modes: 3D, and 2D as the same WebGL scene constrained to a plane. A single
// code path is what makes it worth investing in labels, picking and projection.
//
// One node set, one *semantic* force at a time. Structural forces (link, charge) are always on
// and come from the edges. A semantic force is a positional constraint derived from a metric,
// and only ever one is active: if position encoded two things at once you could not tell whether
// a node sits at the center because it is urgent or because it is popular.
//
// Switching view changes only the target radius, never the angle, so nodes travel along a ray
// and stay trackable. That is what makes a switch read as re-orientation rather than reshuffle.















// ---------------------------------------------------------------------------
// Shapes
// ---------------------------------------------------------------------------







// ---------------------------------------------------------------------------
// Data
// ---------------------------------------------------------------------------

/**
 * Fetches the whole corpus straight from the API.
 *
 * Deliberately not routed through .NET: the payload is megabytes of JSON, and deserialising it
 * into the WASM heap only to hand it straight back to JavaScript would cost more than the
 * request does.
 */
export async function loadFull(elementId, url, force) {
    const it = instances.get(elementId);
    if (!it) return null;

    // A forced load is how a client recovers when its delta cursor is too old to honor.
    if (force) delete it.cache[url];
    const cached = !!it.cache[url];

    if (!it.cache[url]) {
        const res = await fetch(url);
        if (!res.ok) throw new Error(`${url} responded ${res.status}`);
        const data = await res.json().catch(err => {
            // A truncated or non-JSON body reads as a parse error against whatever the browser
            // guessed it was loading, which says nothing about the request that produced it.
            console.error(`abom: ${url} did not return usable JSON`, err);
            throw err;
        });

        const byKey = new Map();
        for (const n of data.nodes) {
            n.refStyles = new Set();
            n.edgeClasses = new Set();
            byKey.set(n.key, n);
        }
        const links = [];
        for (const e of data.edges) {
            const s = byKey.get(e.source), t = byKey.get(e.target);
            if (!s || !t) continue;
            // Classified once here rather than per frame, and recorded on both ends so that
            // filtering by relationship is a set lookup. The store's answer wins where it has
            // one, so the view and the bill of materials cannot disagree.
            e.cls = boundaryClass(e.boundary, s, t);
            s.edgeClasses.add(e.cls);
            t.edgeClasses.add(e.cls);
            // Reference style is a property of the edge, but readers filter on nodes, so it is
            // summarized onto both ends. Lexical only: whether a reference is *safe* is a
            // query's verdict, never this file's.
            for (const ref of e.refs) {
                const style = SHA.test(ref) ? 'sha' : 'tag';
                s.refStyles.add(style);
                t.refStyles.add(style);
            }
            links.push(e);
        }

        const degree = new Map();
        for (const e of links) {
            degree.set(e.source, (degree.get(e.source) || 0) + 1);
            degree.set(e.target, (degree.get(e.target) || 0) + 1);
        }
        const rules = new Set();
        for (const n of data.nodes) {
            for (const r of n.rules || []) rules.add(r);
        }
        it.cache[url] = {
            nodes: data.nodes, links, degree, rules: [...rules].sort(),
            // Which generation this copy reflects. Zero means unknown, which is only true
            // before the page has asked.
            generation: 0,
        };
    }

    it.all = it.cache[url];
    it.degree = it.all.degree;
    it.adjacencies = null;
    // A different node set has to be handed to the renderer rather than merely re-hidden.
    it.seeded = false;
    it.renderedFor = null;
    return {
        nodes: it.all.nodes.length,
        edges: it.all.links.length,
        rules: it.all.rules,
        // Served from a previous visit, positions and all, so the caller knows to catch up on
        // whatever arrived while it was away rather than trusting what it just got.
        cached,
        generation: it.all.generation || 0,
    };
}

/** Records which generation the loaded copy reflects, so a later visit can ask for the rest. */
export function setGeneration(elementId, generation) {
    const it = instances.get(elementId);
    if (it && it.all) it.all.generation = generation;
}

const linkKey = e => `${e.source}\u0000${e.target}`;

// force-graph rewrites an endpoint from a key to the node object once it has drawn, so a removal
// arriving mid-session sees either shape.
const endpointKey = end => (end && typeof end === 'object' ? end.key : end);

// The facts the server reports as either shared by every relationship behind a line or mixed.
// Merging has to reach the same answer, or a line the server would call mixed is drawn asserting
// one of its relationships' facts as the pair's.
const EDGE_FACTS = ['kind', 'boundary', 'refStyle', 'advisoryRoute', 'findingRoute', 'ownedDepth'];

/**
 * Drops the relationships a delta removed, and reports whether any are left to draw.
 *
 * A line stands for every relationship between its endpoints, so removing one of three leaves the
 * line and two relationships. Its references cannot follow: the payload carries them per line
 * rather than per relationship, so a removed reference stays on screen until the next full load.
 */
function survivesRemoval(edge, gone) {
    if (!gone.size || !edge.keys || !edge.keys.length) return true;
    const left = edge.keys.filter(k => !gone.has(k));
    if (!left.length) return false;
    if (left.length !== edge.keys.length) {
        edge.keys = left;
        edge.count = left.length;
    }
    return true;
}

/** Everything in both lists, in the order first seen, without duplicates. */
function union(mine, theirs) {
    const out = [];
    for (const v of [...(mine || []), ...(theirs || [])]) {
        if (!out.includes(v)) out.push(v);
    }
    return out;
}

/** What a line says about a fact: a value, the mixed sentinel, or null for nothing on record. */
const MIXED = Symbol('mixed');
const factOf = (edge, name) => {
    if ((edge.mixed || []).includes(name)) return MIXED;
    const v = edge[name];
    return v === undefined || v === null || v === '' ? null : v;
};

/** Keeps a fact the merged relationships agree on, and names it as mixed when they do not. */
function mergeEdgeFacts(edge, incoming) {
    const mixed = new Set(edge.mixed || []);
    for (const name of EDGE_FACTS) {
        if (factOf(edge, name) === factOf(incoming, name) && !mixed.has(name)) continue;
        // Absent, so a client that ignores `mixed` reads the fact as unknown rather than as a
        // value that is true of one of the relationships behind the line.
        mixed.add(name);
        delete edge[name];
    }
    if (mixed.size) edge.mixed = [...mixed];
}

/**
 * Folds newly discovered nodes and edges into a loaded corpus, in place.
 *
 * Kept separate from the fetch so the decisions it makes are testable: what counts as new, what
 * merely updates, and where an arrival is placed. Nothing here touches the renderer.
 */
export function mergeInto(all, delta) {
    // Removals first: an arriving edge must not attach to a node this delta also takes away.
    const goneNodes = new Set(delta.removedNodes || []);
    const goneEdges = new Set(delta.removedEdges || []);
    if (goneNodes.size || goneEdges.size) {
        all.nodes = all.nodes.filter(n => !goneNodes.has(n.key));
        all.links = all.links.filter(e =>
            survivesRemoval(e, goneEdges) &&
            !goneNodes.has(endpointKey(e.source)) &&
            !goneNodes.has(endpointKey(e.target)));
    }

    const byKey = new Map(all.nodes.map(n => [n.key, n]));
    const arrived = [];
    for (const n of delta.nodes || []) {
        if (byKey.has(n.key)) continue;
        n.refStyles = new Set();
        n.edgeClasses = new Set();
        byKey.set(n.key, n);
        all.nodes.push(n);
        arrived.push(n);
    }

    const existing = new Map(all.links.map(e => [linkKey(e), e]));
    let addedEdges = 0;
    for (const incoming of delta.edges || []) {
        const s = byKey.get(incoming.source), t = byKey.get(incoming.target);
        if (!s || !t) continue;

        let edge = existing.get(linkKey(incoming));
        if (edge) {
            // The same pair seen again carries more relationships, not a second line. A delta
            // sends only what arrived, so replacing rather than adding would forget the ones
            // already drawn and make their later removal match nothing.
            edge.keys = union(edge.keys, incoming.keys);
            edge.refs = union(edge.refs, incoming.refs);
            edge.locked = union(edge.locked, incoming.locked);
            edge.count = edge.keys.length || incoming.count;
            mergeEdgeFacts(edge, incoming);
        } else {
            edge = incoming;
            existing.set(linkKey(edge), edge);
            all.links.push(edge);
            addedEdges++;
        }
        edge.cls = boundaryClass(edge.boundary, s, t);
        s.edgeClasses.add(edge.cls);
        t.edgeClasses.add(edge.cls);
        for (const ref of edge.refs || []) {
            const style = SHA.test(ref) ? 'sha' : 'tag';
            s.refStyles.add(style);
            t.refStyles.add(style);
        }
    }

    // Reach is recomputed wholesale, so every node's counts can move without the node changing.
    for (const m of delta.metrics || []) {
        const n = byKey.get(m.key);
        if (!n) continue;
        n.dependentRepos = m.dependentRepos;
        n.reachKnown = m.reachKnown;
        n.findings = m.findings;
        n.advisories = m.advisories;
        n.analyzed = m.analyzed;
        // The server omits facets where a node holds none, so absent is empty rather than unchanged.
        n.facets = m.facets || [];
        n.rules = [...new Set(n.facets.map(x => x.rule))].sort();
        // Coverage moves for the same reason reach does: a reference analyzed since the last
        // delta changes the ratio without changing the node.
        if (m.refsInUse !== undefined) n.refsInUse = m.refsInUse;
        if (m.refsAnalyzed !== undefined) n.refsAnalyzed = m.refsAnalyzed;
    }

    placeArrivals(all, arrived);

    const degree = new Map();
    for (const e of all.links) {
        degree.set(e.source, (degree.get(e.source) || 0) + 1);
        degree.set(e.target, (degree.get(e.target) || 0) + 1);
    }
    all.degree = degree;
    const rules = new Set();
    for (const n of all.nodes) {
        for (const r of n.rules || []) rules.add(r);
    }
    all.rules = [...rules].sort();

    return { nodes: arrived.length, edges: addedEdges };
}

/**
 * Places arrivals among the neighbors they arrived attached to.
 *
 * Left to the renderer's own seeding they would appear at a hash of their key, which is
 * somewhere arbitrary on the far side of the graph, and the reader would watch them fly across
 * the screen to reach the cluster they belong to.
 */
export function placeArrivals(all, arrived) {
    if (!arrived.length) return;
    const byKey = new Map(all.nodes.map(n => [n.key, n]));

    const neighbors = new Map();
    const note = (a, b) => {
        if (!neighbors.has(a)) neighbors.set(a, []);
        neighbors.get(a).push(b);
    };
    for (const e of all.links) {
        note(e.source, e.target);
        note(e.target, e.source);
    }

    for (const n of arrived) {
        const placed = (neighbors.get(n.key) || [])
            .map(key => byKey.get(key))
            .filter(other => other && other.x !== undefined);
        if (!placed.length) continue;

        const at = axis => placed.reduce((sum, o) => sum + (o[axis] || 0), 0) / placed.length;
        // A little scatter, derived from the key so a reload lands identically: without it
        // every arrival on one hub stacks into a single unreadable point.
        const jitter = radius => (seed(n.key + radius) - 0.5) * 40;
        n.x = at('x') + jitter('#x');
        n.y = at('y') + jitter('#y');
        n.z = at('z') + jitter('#z');
    }
}

/**
 * Fetches what changed and folds it in.
 *
 * A running expansion adds to the corpus continuously. Reloading the whole graph to see that
 * would re-seed every position and destroy the picture the reader is holding, so only the
 * difference is fetched and the layout is reheated rather than rebuilt.
 */
export async function mergeDelta(elementId, url) {
    const it = instances.get(elementId);
    if (!it || !it.all) return null;

    const res = await fetch(url);
    if (!res.ok) {
        // Following runs unattended, so a merge that starts failing has to leave a trace where
        // someone would look for it rather than only stopping.
        console.error(`abom: ${url} responded ${res.status}; the graph is no longer following`);
        throw new Error(`${url} responded ${res.status}`);
    }
    const delta = await res.json();
    if (delta.reset) {
        return { reset: true, generation: delta.generation, nodes: 0, edges: 0 };
    }

    const added = mergeInto(it.all, delta);
    it.all.generation = delta.generation;
    it.degree = it.all.degree;
    it.adjacencies = null;
    // The node set handed to the renderer has changed, so a repaint alone would leave the
    // arrivals invisible however correct the filter result is.
    it.dirty = added.nodes > 0 || added.edges > 0;
    // A merge must never move the camera: the reader is mid-thought about what is on screen.
    it.keepView = true;

    return {
        reset: false,
        generation: delta.generation,
        nodes: added.nodes,
        edges: added.edges,
        rules: it.all.rules,
    };
}

/**
 * Adjacency in one direction or both.
 *
 * Direction is the difference between "what does this pull in" and "who is waiting on this",
 * and they are different questions with different answers. Walking undirected answers neither:
 * a queued dependency has no outgoing edges at all, so an undirected hop shows only its callers
 * while implying it showed everything.
 */
function adjacencyOf(it, direction = 'both') {
    it.adjacencies ??= {};
    if (it.adjacencies[direction]) return it.adjacencies[direction];

    const adj = new Map();
    const link = (a, b) => {
        if (!adj.has(a)) adj.set(a, []);
        adj.get(a).push(b);
    };
    for (const e of it.all.links) {
        if (direction !== 'in') link(e.source, e.target);
        if (direction !== 'out') link(e.target, e.source);
    }
    it.adjacencies[direction] = adj;
    return adj;
}

/**
 * The nodes a focus key names.
 *
 * A dependency the queue fetched at several references is split into `name@ref` nodes and has no
 * bare key; one fetched at a single reference keeps it. Both shapes are in the corpus, so a page
 * handing over an action name — Remediation's Explore link is the one that does — resolves for some
 * dependencies and finds nothing for exactly the widely-referenced ones it is most often about.
 * Returns empty when the key names nothing, which the caller has to say rather than draw.
 */
export function focusKeys(it, key) {
    if (!key || !it || !it.all) return [];
    const nodes = it.all.nodes;
    if (nodes.some(n => n.key === key)) return [key];
    // `@` and not a bare prefix: `other/checkout@v1` is a different action from `checkout`.
    const prefix = `${key}@`;
    return nodes.filter(n => n.key.startsWith(prefix)).map(n => n.key).sort();
}

/** Everything reachable from a selection, out to a hop limit, following the given direction. */
export function neighborhood(it, key, hops, direction) {
    const adj = adjacencyOf(it, direction);
    const roots = Array.isArray(key) ? key : [key];
    const seen = new Map(roots.map(k => [k, 0]));
    let frontier = [...roots];
    const limit = hops > 0 ? hops : Infinity;
    for (let hop = 1; hop <= limit && frontier.length; hop++) {
        const next = [];
        for (const node of frontier) {
            for (const other of adj.get(node) || []) {
                if (seen.has(other)) continue;
                seen.set(other, hop);
                next.push(other);
            }
        }
        frontier = next;
    }
    return seen;
}

/**
 * Narrows the loaded graph.
 *
 * A selection's neighborhood is walked over the *whole* graph and the attribute filters are
 * then applied to the result. Walking only over already-filtered nodes would cut the path to
 * anything interesting and silently under-report reach.
 */
/**
 * Whether every reference of a dependency has been analyzed.
 *
 * `analyzed` is true as soon as one reference has results, which is the right answer to "is
 * anything downstream of this known" and the wrong one to "has this been covered". The queue
 * analyzes a reference and the graph draws a name, so the counts are what tell 1 of 27 from 27
 * of 27. Zero references is nothing looked at, not everything covered.
 */
export function fullyAnalyzed(n) {
    if (n.refsInUse > 0) return (n.refsAnalyzed || 0) >= n.refsInUse;
    if (n.refsInUse === 0 && n.refsAnalyzed !== undefined) return false;
    return !!n.analyzed;
}

/**
 * The nodes a filtered set depends on, so a relationship has both of its ends.
 *
 * An edge is drawn only when both ends survive, and a filter keeping a sixth of the nodes kept 39
 * of 7,552 relationships on the real corpus — a dependency graph with no dependencies in it.
 *
 * Outward only. What a shown node *uses* was worth 2,947 edges and what uses it 469, and the page
 * exists to answer "what do I depend on that something is wrong with" rather than who depends on me.
 */
export function withDependencies(visible, links, edgeClasses) {
    const context = new Set();
    for (const e of links) {
        if (!edgeClasses.has(e.cls)) continue;
        if (visible.has(e.source) && !visible.has(e.target)) context.add(e.target);
    }
    return context;
}

/**
 * Which arrival-scope filters still apply, and whether failing one removes a node or greys it.
 *
 * None of them apply once the reader has named a node. Isolating asked for a neighbourhood of 23
 * nodes and 22 edges on the real corpus and drew **1 node and 0 edges**, because `has findings` was
 * applied on top and none of the neighbours had one — so the gesture that means "show me what this
 * touches" deleted everything it touches. Kinds, origins, search and rules are deliberate choices
 * about rendering and stay; these four decide which nodes are worth showing, which is the question
 * the reader has just answered.
 *
 * `removes` is the boundaries-or-relationships trade, and grouping is what settles it. A boundary
 * drawn around only the nodes that matched claims the group is *only* those, so a grouped view has
 * to remove what it filters — and an edge needs both its ends, which is why removing cost 7,513 of
 * 7,552 relationships. Ungrouped there is no boundary making that claim, so nothing has to go.
 */
export function scopeFilters(f, resolved = true) {
    if (f.selection && resolved) {
        return {
            hasFindings: false, hasAdvisory: false, unanalyzedOnly: false, minReach: 0,
            removes: false,
        };
    }
    return {
        hasFindings: !!f.hasFindings,
        hasAdvisory: !!f.hasAdvisory,
        unanalyzedOnly: !!f.unanalyzedOnly,
        minReach: f.minReach || 0,
        removes: f.grouped !== false,
    };
}

/**
 * Whether a node answers the questions the arrival scope asks of it.
 *
 * Failing one is not always a reason to remove it. An edge needs both of its ends, so cutting the
 * nodes that do not answer takes the relationships with them -- `has findings` is on by default,
 * and left 39 of 7,552 edges on the real corpus, a view in which almost nothing could be followed
 * anywhere. Whether this removes or merely greys is `scope.removes`, which grouping decides.
 */
export function answersScope(node, scope) {
    if (scope.hasFindings && !node.findings) return false;
    if (scope.hasAdvisory && !node.advisories) return false;
    if (scope.unanalyzedOnly && fullyAnalyzed(node)) return false;
    if (scope.minReach && (node.dependentRepos || 0) < scope.minReach) return false;
    return true;
}

/** A rule pattern as the set path reads one: a trailing `*` is a prefix, anything else exact. */
export function ruleMatches(patterns, rule) {
    return patterns.some(p => p.endsWith('*') ? rule.startsWith(p.slice(0, -1)) : rule === p);
}

/**
 * Whether a node answers the finding terms, read per finding from the kinds it carries whole, so
 * `rules=A&severity=error` keeps a node only where one finding is both. An excluded rule drops the
 * node outright, and asking for none of them narrows nothing.
 */
export function answersFindingTerms(node, f) {
    const facets = node.facets || [];
    const rules = f.rules || [];
    const notRules = f.notRules || [];
    const bands = f.bands || [];
    if (notRules.length && facets.some(x => ruleMatches(notRules, x.rule))) return false;
    if (!rules.length && !f.provider && !f.severity && !bands.length) return true;
    return facets.some(x =>
        (!rules.length || ruleMatches(rules, x.rule)) &&
        (!f.provider || x.provider === f.provider) &&
        (!f.severity || x.severity === f.severity) &&
        (!bands.length || bands.includes(x.band)));
}

export function applyFilters(elementId, spec) {
    const it = instances.get(elementId);
    if (!it || !it.all) {
        return { nodes: 0, edges: 0, hidden: 0, total: 0, edgeTotal: 0, context: 0 };
    }

    const f = spec || {};
    const total = it.all.nodes.length;
    const subjects = f.selection ? focusKeys(it, f.selection) : [];
    const reachable = subjects.length
        ? neighborhood(it, subjects, f.hops || 0, f.direction || 'both')
        : null;

    const search = (f.search || '').toLowerCase();
    // Explicit allow-lists: the panel always sends what is checked, so an empty list genuinely
    // means "show none" rather than "no opinion".
    const kinds = new Set(f.kinds || []);
    // Links saved before the classes were renamed still name the old keys; accepting both keeps
    // an old address from silently emptying the graph.
    const edgeClasses = new Set((f.edgeClasses || []).map(c =>
        c === 'externalOrg' ? 'crossOwner' : c === 'sameOrg' ? 'sameOwner' : c));
    const origins = new Set(f.origins || []);
    // A focus that named nothing has narrowed nothing, so the flags it would have suspended still
    // apply: suspending them anyway draws the whole graph with its default filter switched off.
    const scope = scopeFilters(f, subjects.length > 0);

    const visible = new Set();
    // What the reader asked for, as opposed to what is merely on screen so an edge has both ends.
    const answers = new Set();
    for (const n of it.all.nodes) {
        if (reachable && !reachable.has(n.key)) continue;
        if (!kinds.has(n.kind)) continue;
        if (origins.size && !origins.has(n.origin || 'external')) continue;
        if (search && !n.key.toLowerCase().includes(search)) continue;
        if (!answersFindingTerms(n, f)) continue;
        if (f.refStyle && f.refStyle !== 'any' && !n.refStyles.has(f.refStyle)) continue;
        const answer = answersScope(n, scope);
        if (scope.removes && !answer) continue;
        visible.add(n.key);
        if (answer) answers.add(n.key);
    }

    // Drawn so a relationship has both ends, not because they matched what the reader asked for,
    // so they are marked and dimmed rather than presented as answers.
    const context = f.dependencies === false
        ? new Set()
        : withDependencies(visible, it.all.links, edgeClasses);
    for (const key of context) visible.add(key);
    for (const n of it.all.nodes) n.context = visible.has(n.key) && !answers.has(n.key);

    let edges = 0;
    // force-graph holds copies of the links, so a flag written only onto the originals never
    // reaches the renderer: `l.shown` stayed undefined and every edge drew regardless of what
    // the filter said. The copies are keyed by their original so filtering cannot misalign them.
    for (const e of it.all.links) {
        e.shown = edgeClasses.has(e.cls) && visible.has(e.source) && visible.has(e.target);
        const copy = it.renderedFor && it.renderedFor.get(e);
        if (copy) copy.shown = e.shown;
        if (e.shown) edges++;
    }

    // Isolating replaces the selection with the node it was performed on: it is a different
    // question from comparison, and keeping several marked inside one node's subgraph would
    // mean nothing. A split dependency is one subject drawn as several nodes, so all of them are
    // the selection.
    it.selected = new Set(subjects);
    it.hopsOf = reachable;
    it.visible = visible;
    it.contextKeys = new Set([...visible].filter(k => !answers.has(k)));
    it.visibleSig = visibleSignature(visible);
    wake(it);
    // Kept for the fold: a merged edge is classified only once the groups exist, so it has to be
    // held to the same filter the edges it was merged from were.
    it.edgeClasses = edgeClasses;
    // Isolating already removed the unrelated, so dimming on top of it would only mute what is
    // left. Emphasis belongs to selection-without-narrowing.
    it.emphasis = null;

    // Anything hidden is left out of the simulation entirely. Keeping it in so that positions
    // held still across a filter cost 3.4 frames per second on the full corpus: `nodeVisibility`
    // only stops a node being drawn, while d3-force went on integrating all 9,811 of them every
    // tick. Coordinates live on the nodes themselves, so returning to a wider filter is free.
    const decision = drawDecision(visible.size, it.drawApproved);
    if (!decision.draw) {
        withholdRender(it);
    } else if (needsRender(it)) {
        render(it);
    } else {
        paint(it);
        it.graph.refresh();
    }
    if (it.collapsed) {
        const shown = it.graph.graphData();
        return {
            nodes: shown.nodes.length, edges: shown.links.length, hidden: 0, total,
            edgeTotal: shown.links.length, context: 0, withheld: decision.withheld,
        };
    }
    // An edge needs both its ends, so a filter that keeps a sixth of the nodes keeps far less than
    // a sixth of the relationships. The view has to be able to say so.
    return { nodes: visible.size, edges, hidden: total - visible.size, total,
        edgeTotal: it.all.links.length, context: it.contextKeys.size,
        // What the ceiling held back. Zero is drawn; anything else is a picture nobody has seen.
        withheld: decision.withheld,
        // Zero subjects for a named focus is a key this graph does not hold, which is a different
        // answer from a neighborhood that happens to be empty.
        focusFound: f.selection ? subjects.length : 0 };
}

/**
 * Empties the canvas rather than leaving the previous picture under a refusal.
 *
 * A stale graph beneath a panel saying the current one is too large reads as the current one, and
 * the reader would be interpreting a filter they have already moved on from.
 */
function withholdRender(it) {
    it.graph.graphData({ nodes: [], links: [] });
    // Nothing on screen matches the filter, so approval has to take the full render path.
    it.seeded = false;
    it.renderedSig = null;
    it.renderedFor = null;
    it.regionSig = undefined;
}

/**
 * Accepts a graph past the ceiling, for this view until it is replaced.
 *
 * Deliberately not remembered across visits: the acceptance is of one picture a reader chose to
 * look at, not a standing preference for hairballs.
 */
export function approveLargeGraph(elementId, spec) {
    const it = instances.get(elementId);
    if (!it || it.drawApproved) return;
    it.drawApproved = true;
    return applyFilters(elementId, spec);
}

/**
 * The largest node set this view will draw without being told to.
 *
 * A force-directed picture stops carrying information well before it stops rendering. Past a few
 * hundred nodes the reader is looking at a hairball: boundaries interleave and are refused, labels
 * have nowhere to go, and no layout engine offered here produces a shape worth reading — `dot`
 * abandons 3,703 nodes outright. The useful question at that size is not "what does all of this
 * look like" but "what reaches this advisory", and the way to it is the filters.
 */
export const DRAW_CEILING = 300;

/**
 * Whether a node set is drawn, and how much is being withheld if not.
 *
 * A refusal rather than a warning: a caution printed over a drawn hairball is a note nobody reads
 * beside a picture they are already trying to interpret. The ceiling is inclusive, because a
 * reader who narrowed to exactly it asked for something the number promised them.
 */
export function drawDecision(count, approved, ceiling = DRAW_CEILING) {
    if (approved || count <= ceiling) return { draw: true, withheld: 0 };
    return { draw: false, withheld: count };
}

/**
 * A cheap identity for the visible set, so an unchanged filter can be repainted.
 *
 * Order-independent and size-sensitive: two selections of the same size are routinely different
 * sets, and comparing counts alone would leave the simulation holding the previous one.
 */
export function visibleSignature(visible) {
    let sum = 0;
    for (const key of visible) sum = (sum + seed(key) * 4294967296) % 9007199254740881;
    return `${visible.size}:${sum}`;
}

/**
 * Whether the node set has to be rebuilt rather than repainted.
 *
 * A fold is a function of the filtered nodes, the filtered edges and the grouping, and it is
 * built during a render. So a collapsed graph can never take the repaint path: doing so left
 * the previous fold on screen under the new grouping's name, and kept showing merged edges
 * whose underlying edges the filter had just removed.
 */
export function needsRender(it) {
    return !!it.dirty || !it.seeded || it.visibleSig !== it.renderedSig || !!it.collapsed;
}

/** The nodes and links handed to the simulation: what is visible, never the whole corpus. */
export function simulationSet(it) {
    if (!it.visible) return { nodes: it.all.nodes, links: it.all.links };
    return {
        nodes: it.all.nodes.filter(n => it.visible.has(n.key)),
        links: it.all.links.filter(e => e.shown),
    };
}

/** How long a settled view keeps drawing at full rate before it comes to rest. */
export const REST_DELAY = 2000;

/** Frames per second at rest. Enough for a particle to read as moving, not as a video. */
export const REST_FPS = 6;

// How far from the pointer a node still gets named, and how many carry a name unprompted. The
// radius is what makes a dense patch readable by moving over it; the budget is what stops the
// unprompted ones becoming the clutter they were meant to cure.
const LABEL_RADIUS = 110;
const LABEL_TOP = 10;
// How many labels the halo names at once, and how many a selection may. Both are budgets against
// the same failure: a column of boxes over the node the reader is trying to read.
const LABEL_HALO_MAX = 5;
const SELECTED_LABEL_MAX = 4;
const HALO_MIN_ALPHA = 0.45;

// Room left around the core when the camera frames it, in screen pixels: a boundary is drawn
// outside its members and carries a name above that, and fitting the members alone clipped both.
const FIT_GUTTER = 28;

// The narrowest and widest a boundary's gap may be, in screen pixels. Between them it is a share
// of the cluster's own extent, so it reads the same at any zoom.
const REGION_FLOOR = 7;
const REGION_CEILING = 34;

// How near the pointer a node has to be to be the one it is asking about. A node is drawn at half
// a pixel, so "under the pointer" cannot mean what the renderer's hit test means — and a radius
// tight enough to feel precise found nothing at most pixels, which read as the panel being stuck.
const PICK_RADIUS = 44;

// How far a press may travel and still be a click. Below a trackpad's own drift, above nothing.
const CLICK_SLOP = 5;

/**
 * How much per-frame work a view can drop, given nothing has changed for a while.
 *
 * The layout stops itself, but the renderer and the label pass went on redrawing an identical
 * frame for ever. `still` stops both; `idle` keeps drawing a few times a second because
 * directional particles mark which way a dependency points, and that is worth keeping on a view
 * nobody is touching — they simply advance in steps rather than smoothly.
 */
export function restLevel(it, now) {
    if (!it.settledAt || it.dirty) return 'none';
    if (now - it.settledAt < REST_DELAY) return 'none';
    return it.animate && it.selected && it.selected.size ? 'idle' : 'still';
}

/**
 * Brings the view back to full rate.
 *
 * Called from anything that changes what a frame would show: a filter, a merge, a hover, or the
 * camera moving. Cheap enough to call unconditionally.
 */
export function wake(it) {
    if (!it) return;
    it.settledAt = 0;
    if (it.resting === 'still' && it.graph) it.graph.resumeAnimation();
    const wasResting = it.resting;
    it.resting = 'none';
    if (wasResting && wasResting !== 'none' && !it.drawing) drawLabels(it);
}

/**
 * Where the camera is, cheaply enough to compare every frame.
 *
 * Stillness is detected by comparing this rather than by listening for the controls' change
 * event: damping keeps that event firing long after the reader has stopped, so a view driven by
 * it never came to rest at all.
 */
function cameraSignature(it) {
    const c = it.graph && it.graph.camera && it.graph.camera();
    const p = c && c.position;
    const q = c && c.quaternion;
    if (!p || !q || typeof p.x !== 'number' || typeof q.x !== 'number') return '';
    return `${p.x.toFixed(1)},${p.y.toFixed(1)},${p.z.toFixed(1)},`
         + `${q.x.toFixed(3)},${q.y.toFixed(3)},${q.z.toFixed(3)},${q.w.toFixed(3)}`;
}

/** Seeds positions and hands the node set to the renderer. */
function render(it) {
    let { nodes, links } = simulationSet(it);
    let collapsed = null;
    if (it.collapsed) {
        collapsed = collapseGroups(it, nodes, it.all.links);
        nodes = collapsed.nodes;
        links = collapsed.links;
    }
    it.renderedSig = it.visibleSig;
    // A new node set is a new layout, so the view is moving again until the engine says otherwise.
    it.engineStopped = false;
    it.settledAt = 0;
    it.maxPriority = Math.max(1, ...nodes.map(n => n.priority || 0));

    // The layout's natural span grows with the node count, so the seed radius has to as well.
    // Seeding thousands of nodes into a small ball leaves the simulation pushing outward from a
    // singularity, and it settles as a speck.
    const spread = Math.max(300, Math.sqrt(nodes.length) * 34);
    const box = it.el ? it.el.getBoundingClientRect() : null;
    const aspect = box && box.height > 0 ? box.width / box.height : 1;
    let fresh = 0;
    for (const n of nodes) {
        if (n.x === undefined) {
            fresh++;
            Object.assign(n, seedAt(n.key, spread, aspect, it.use3d));
        }
    }
    it.spread = spread;
    // Switching payloads or narrowing returns to nodes that already hold settled coordinates.
    // Paying a full warmup again would shake a picture that is already correct.
    it.settled = nodes.length > 0 && fresh < nodes.length * 0.2;
    applySettleBudget(it);

    // force-graph rewrites link endpoints into object references, so it gets fresh copies and
    // the loaded graph keeps its string keys for filtering.
    it.renderedFor = new Map();
    const copies = links.map(e => {
        const c = { ...e };
        it.renderedFor.set(e, c);
        return c;
    });
    it.graph.graphData({ nodes, links: copies });
    it.seeded = true;
    it.dirty = false;
    // A filter rebuilds the node set, so a node arriving without its pin would be handed back to
    // the simulation under a layout the caption still calls stable. The frame is retaken with it,
    // because the drawn set is what the canvas is sized for.
    if (it.layout) applyLayoutFrame(it);
    // A merge into a graph the reader is already reading must not move the camera under them.
    it.pendingFit = !it.keepView;
    it.keepView = false;
    applyForces(it);
    paint(it);
}

/**
 * Splits the layout between ticks that are computed and ticks that are drawn.
 *
 * d3 starts at alpha 1 and decays, so the earliest frames are the ones where nodes move
 * furthest. Rendering from tick zero therefore shows the reader the most violent part of the
 * settle and hides the calm part. With animation off, nearly all of it is computed before the
 * first frame is drawn.
 */
function applySettleBudget(it) {
    if (it.settled) {
        // Coordinates carried over from a previous payload only need the newcomers placed.
        it.graph.warmupTicks(0).cooldownTicks(60);
    } else if (it.animate) {
        it.graph.warmupTicks(90).cooldownTicks(360);
    } else {
        it.graph.warmupTicks(420).cooldownTicks(30);
    }
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

const CAPTION_GUTTER = 64;

// The shortest graph worth drawing. Below this the force layout has nowhere to separate anything
// and the view reads as a smear rather than a structure.
const MIN_GRAPH_HEIGHT = 360;

/**
 * How tall the graph host should be, given where it sits in the window.
 *
 * `top` is viewport-relative, as `getBoundingClientRect()` reports it, and scroll position is
 * deliberately not an input: the host's distance from the bottom of the window is what decides how
 * much room it has, and that does not change because the document moved under it. Adding
 * `window.scrollY` here made the height shrink as the reader scrolled, collapsing a 700px graph to
 * its 360px floor on every resize after the first.
 *
 * A host scrolled above the top of the window is clamped to zero rather than allowed to go
 * negative, which would otherwise make it taller than the window it is being fitted to.
 */
export function graphHeight({ top, innerHeight, gutter = CAPTION_GUTTER, floor = MIN_GRAPH_HEIGHT }) {
    const below = innerHeight - Math.max(top, 0) - gutter;
    return Math.max(floor, Math.round(below));
}

/**
 * Whether the host's height should be rewritten for the room it now has.
 *
 * Resizing a WebGL canvas reallocates its drawing buffer, so doing it for every pixel of scroll
 * costs frames and visibly flickers. Only a difference a reader would notice is worth paying for.
 */
export function refitNeeded(current, desired, tolerance = 24) {
    return Math.abs(desired - current) >= tolerance;
}

function fitHost(el) {
    if (!el || !el.isConnected) return;
    el.style.height = `${graphHeight({
        top: el.getBoundingClientRect().top,
        innerHeight: window.innerHeight,
    })}px`;
}

const EDITABLE_TAGS = new Set(['INPUT', 'TEXTAREA', 'SELECT']);

/**
 * Whether an Escape keypress is the reader asking to leave the graph's selection.
 *
 * Escape already means "close this" to a search box or an open menu. Acting on it there too
 * would throw away the surrounding context as a side effect of dismissing something small.
 */
export function escapesGraph(key, target, overlayOpen) {
    if (key !== 'Escape' || overlayOpen) return false;
    if (!target) return true;
    if (EDITABLE_TAGS.has(target.tagName)) return false;
    return !target.isContentEditable;
}

// MudBlazor marks its open menus and dialogs, and answers Escape on them itself.
function overlayOpen() {
    return !!document.querySelector('.mud-popover-open, .mud-dialog-container');
}

export function create(elementId, use3d, dotNetRef) {
    const previous = instances.get(elementId);
    const carried = previous ? { cache: previous.cache, all: previous.all } : { cache: {} };
    dispose(elementId, true);

    const el = document.getElementById(elementId);
    if (!el || !window.ForceGraph3D) return false;

    fitHost(el);
    const box = el.getBoundingClientRect();

    const graph = window.ForceGraph3D()(el)
        .backgroundColor('#00000000')
        .width(Math.max(1, Math.round(box.width)))
        .height(Math.max(1, Math.round(box.height)))
        .numDimensions(use3d ? 3 : 2)
        .nodeId('key')
        // Disabled deliberately: force-graph's tooltip is an HTML element positioned from its own
        // hover events, and this view already draws labels on the canvas for hover AND selection.
        // Running both put two labels for one node on screen, and the tooltip lingered on the
        // last node it saw once a selection existed — it has no notion of ours.
        .nodeLabel(() => '')
        .nodeVal(nodeRadius)
        // Four segments is plenty for a sphere a few pixels across, and at thousands of nodes
        // triangle count is what decides whether the view stays interactive.
        .nodeResolution(4)
        // Any width above zero replaces cheap lines with per-link tube geometry, which at ten
        // thousand links is the difference between interactive and not. Weight shows as opacity.
        .linkWidth(0)
        .linkOpacity(0.3)
        .enableNodeDrag(false)
        // Medium resistance. The default lets a large graph overshoot and oscillate, which is
        // what reads as wobble while it settles.
        .d3VelocityDecay(0.55)
        .onNodeHover(n => {
            const cur = instances.get(elementId);
            // Kept for the double-click test, which needs to know whether the gesture landed on
            // empty space. What the panel and the labels describe is decided in `drawLabels`, by
            // screen distance, because this fires on a sphere half a pixel across.
            if (cur) cur.hovered = n ? n.key : '';
            wake(cur);
        })
        .warmupTicks(60)
        .cooldownTicks(400)
        .onEngineStop(() => {
            const cur = instances.get(elementId);
            if (!cur) return;
            if (cur.pendingFit) {
                cur.pendingFit = false;
                fitCore(cur);
            }
            // The renderer has now built its default meshes, so the material class can be
            // harvested and the real shapes applied.
            if (cur.shapes && cur.materialClass === undefined && internalMaterialClass(cur)) {
                paint(cur);
                cur.graph.refresh();
            }
            // The layout takes seconds to settle on a large graph, and until it does the canvas
            // looks blank rather than busy. Only the simulation knows when it is done.
            if (dotNetRef) dotNetRef.invokeMethodAsync('OnLayoutSettled');
            cur.engineStopped = true;
        });

    const controls = graph.controls();
    if (controls) {
        controls.zoomSpeed = 2.4;
        // In 2D the scene is a plane. Left free to rotate it can be tumbled edge-on, where it
        // occupies no pixels and reads as a blank canvas.
        controls.noRotate = !use3d;
        if (controls.mouseButtons) {
            Object.assign(controls.mouseButtons, buttonMap(controls.mouseButtons, use3d));
        }
    }
    // The library writes its own control hint and has no API for the text, so it does not know
    // about the ones added here. A hint that lists some of the controls is worse than none.
    // Measured before promising it: 51% of the canvas has nothing within the pick radius, so
    // there is somewhere to double-click.
    const nav = el.querySelector('.scene-nav-info');
    if (nav) {
        nav.textContent = use3d
            ? 'Drag: rotate · Right-drag: pan · Scroll: zoom · Double-click empty space: zoom in · Right-click a node: isolate'
            : 'Drag: pan · Scroll: zoom · Double-click empty space: zoom in · Right-click a node: isolate';
    }
    graph.cameraPosition({ x: 0, y: 0, z: 1200 }, { x: 0, y: 0, z: 0 });

    const labels = document.createElement('canvas');
    labels.className = 'abom-graph-labels';
    el.appendChild(labels);

    // Zoom on a real double-click rather than on two background clicks: the renderer reports a
    // click per press, so a pair arrives as two independent "you clicked nothing" events with no
    // way to tell them from a reader clicking twice to dismiss something. `hovered` is the
    // renderer's own hit test, so an empty-space gesture is the one with nothing under it --
    // double-clicking a node already isolates it and must keep doing so.
    // A node is drawn at half a pixel, so the renderer reports empty space for a gesture aimed at
    // one. `picked` is what the reader is pointing at.
    const onDoubleClick = () => {
        const cur = instances.get(elementId);
        if (cur && !cur.hovered && !cur.picked) zoomBy(cur, 0.6);
    };
    el.addEventListener('dblclick', onDoubleClick);

    const observer = new ResizeObserver(entries => {
        for (const e of entries) {
            const r = e.contentRect;
            if (r.width > 0 && r.height > 0) {
                graph.width(Math.round(r.width)).height(Math.round(r.height));
            }
        }
    });
    observer.observe(el);

    const onResize = () => fitHost(el);
    window.addEventListener('resize', onResize);
    requestAnimationFrame(() => fitHost(el));

    // The height is the room below the host, and scrolling changes that. The graph opens part way
    // down a page that scrolls, so it was sized for a position the reader leaves immediately --
    // 360px, the floor, against the 536px it has once actually in view. Settled rather than live,
    // because refitting mid-gesture reallocates the drawing buffer on every frame. Growing the
    // host moves only what is below it, so its own top does not move and this converges in one
    // step rather than chasing itself.
    let settle = null;
    const onScroll = () => {
        clearTimeout(settle);
        settle = setTimeout(() => {
            if (!el.isConnected) return;
            const box = el.getBoundingClientRect();
            const desired = graphHeight({ top: box.top, innerHeight: window.innerHeight });
            if (refitNeeded(box.height, desired)) fitHost(el);
        }, 150);
    };
    window.addEventListener('scroll', onScroll, { passive: true });

    // At rest the label loop returns without rescheduling and the renderer is paused, so nothing
    // is polling the camera any more -- a drag moves a camera whose frame nobody draws, and the
    // view reads as frozen. Hover cannot rescue it either, because force-graph raycasts inside
    // the loop that was paused. A gesture on the canvas is the reader asking for it back.
    const onWake = () => wake(instances.get(elementId));
    el.addEventListener('wheel', onWake, { passive: true });

    // Every gesture below resolves through `picked` -- the same nearest-node answer the ring, the
    // labels and the metadata panel already use. The renderer's own hit test is not consulted,
    // because a node is drawn at half a pixel: it reports empty space for a gesture aimed dead
    // centre at one, which left right-click unreachable and made double-click depend on whether
    // the two presses happened to agree. The ring is what the reader is aiming at, so the ring is
    // what every gesture acts on.
    const onPointerDown = event => {
        const cur = instances.get(elementId);
        if (!cur) return;
        cur.pressedAt = { x: event.clientX, y: event.clientY };
        wake(cur);
    };
    const onClick = event => {
        const cur = instances.get(elementId);
        if (!cur || !isClick(cur.pressedAt, { x: event.clientX, y: event.clientY }, CLICK_SLOP)) return;
        if (cur.picked) {
            canvasClick(cur, cur.picked, event, dotNetRef);
            return;
        }
        if (dotNetRef) dotNetRef.invokeMethodAsync('OnGraphCleared');
    };
    // Isolating is decided on release, not here: this fires on the press, and right-drag is how
    // the reader pans, so acting now would isolate a node every time they moved the view.
    const onContextMenu = event => event.preventDefault();
    const onPointerUp = event => {
        const cur = instances.get(elementId);
        if (!cur || event.button !== 2) return;
        if (!isClick(cur.pressedAt, { x: event.clientX, y: event.clientY }, CLICK_SLOP)) return;
        if (cur.picked && dotNetRef) dotNetRef.invokeMethodAsync('OnNodeIsolated', cur.picked);
    };
    el.addEventListener('pointerdown', onPointerDown);
    el.addEventListener('pointerup', onPointerUp);
    el.addEventListener('click', onClick);
    el.addEventListener('contextmenu', onContextMenu);

    // The pointer is kept in host coordinates because labels are drawn on an overlay that shares
    // them. Waking on every move is what makes the halo follow the reader; when they stop moving
    // the events stop and the view settles again on its own.
    const onPointerMove = event => {
        const cur = instances.get(elementId);
        if (!cur) return;
        const r = el.getBoundingClientRect();
        cur.pointer = { x: event.clientX - r.left, y: event.clientY - r.top };
        wake(cur);
    };
    const onPointerLeave = () => {
        const cur = instances.get(elementId);
        if (!cur) return;
        cur.pointer = null;
        wake(cur);
    };
    el.addEventListener('pointermove', onPointerMove);
    el.addEventListener('pointerleave', onPointerLeave);

    // On document rather than the canvas: the WebGL surface never holds focus, so a listener
    // bound to it would only fire after the reader had clicked it first.
    const onKeyDown = event => {
        if (!escapesGraph(event.key, event.target, overlayOpen())) return;
        if (dotNetRef) dotNetRef.invokeMethodAsync('OnGraphCleared');
    };
    document.addEventListener('keydown', onKeyDown);

    const it = {
        graph, observer, onResize, onScroll, onWake, onDoubleClick, onPointerMove, onPointerLeave,
        onPointerDown, onPointerUp, onClick, onContextMenu,
        onKeyDown, el, labels, use3d,
        dotNetRef,
        cache: carried.cache, all: carried.all, adjacency: null,
        degree: carried.all ? carried.all.degree : new Map(),
        selected: new Set(), hovered: '', picked: '', pressedAt: null,
        hopsOf: null, view: 'neighborhood', palette: null,
        emphasis: null,
        maxPriority: 1, pendingFit: false, running: true, seeded: false, visible: null,
        subset: false, renderedFor: null, groupBy: 'owner', collapsed: false,
        id: elementId, particleTimer: null, particlePhase: 0,
        animate: previous ? previous.animate !== false : true,
        // Proved at runtime rather than assumed: the graph library bundles its own THREE, so
        // meshes built from the vendored copy have to be shown to actually render.
        shapes: shapesUsable(),
    };
    instances.set(elementId, it);
    drawLabels(it);
    return true;
}

/**
 * Labels are drawn on a 2D canvas over the WebGL surface rather than as sprites in the scene.
 *
 * Text stays crisp at any zoom, and only the handful of labels worth showing costs anything — a
 * sprite per node would put thousands of extra objects into a scene that has to stay
 * interactive.
 */
/**
 * Which nodes carry text, and how much of it.
 *
 * `detail` is the node under the pointer and the selection: name plus where it lives. `name` is
 * everything near the pointer, so a dense patch becomes readable by moving over it, plus the
 * widest-reaching nodes so the view is never mute before the reader touches it.
 *
 * An unlabelled node is a rendering budget, never a judgement — which is also why reach a node
 * does not have cannot buy it a slot: `reachKnown` is false for anything ingested since the last
 * rebuild, and ranking it as zero would assert a measurement nobody made.
 */
export function labelTiers(nodes, { hovered, selected, pointer, radius = 0, top = 0, haloMax = 0, selectedMax = 0 } = {}) {
    const tiers = new Map();

    // The halo is for scanning a dense patch. Once the reader has landed on something it gets out
    // of the way, because six boxes of a repeated path prefix cover the very node being aimed at.
    if (pointer && radius > 0 && !hovered) {
        const r2 = radius * radius;
        const near = [];
        for (const n of nodes) {
            const dx = n.sx - pointer.x, dy = n.sy - pointer.y;
            const d2 = dx * dx + dy * dy;
            if (d2 <= r2) near.push({ key: n.key, d2 });
        }
        // Nearest few rather than everything in range: an uncapped halo in the middle of the graph
        // puts a column of names over the node the reader is aiming at.
        near.sort((a, b) => a.d2 - b.d2 || (a.key < b.key ? -1 : 1));
        const kept = haloMax > 0 ? near.slice(0, haloMax) : near;
        for (const n of kept) tiers.set(n.key, 'name');
    }

    // The widest-reaching nodes keep their names through a hover. They sit away from the pointer
    // and are what stops the view being mute, so suppressing them would blank the whole picture to
    // read one node.
    if (top > 0) {
        const ranked = nodes.filter(n => n.reachKnown && n.dependentRepos > 0)
            .sort((a, b) => b.dependentRepos - a.dependentRepos || (a.key < b.key ? -1 : 1));
        for (const n of ranked.slice(0, top)) tiers.set(n.key, 'name');
    }

    // Last, so detail outranks a name the passes above already gave the same node. A selection is
    // the reader's own choice and survives a hover somewhere else — but it is not always a list of
    // choices: one dependency analyzed at 35 references is one subject drawn as 35 nodes, and
    // labelling each stacked a column of boxes over the middle naming versions nobody picked.
    // Ranked by reach, because the reference most repositories actually use is the one worth
    // naming; the rest stay visibly selected, which is what says they are in the set.
    let chosen = [...(selected || [])];
    if (selectedMax > 0 && chosen.length > selectedMax) {
        const reachOf = new Map(nodes.map(n => [n.key, n.dependentRepos || 0]));
        chosen.sort((a, b) =>
            (reachOf.get(b) || 0) - (reachOf.get(a) || 0) || (a < b ? -1 : 1));
        chosen = chosen.slice(0, selectedMax);
    }
    for (const key of chosen) tiers.set(key, 'detail');
    if (hovered) tiers.set(hovered, 'detail');
    return tiers;
}

/**
 * A node's name with the part a boundary already states removed.
 *
 * Inside a region labelled `actions/setup-python`, `check-dist.yml` is unambiguous and about a
 * quarter the width. What is dropped is the container and the `.github/workflows` boilerplate every
 * workflow carries, neither of which tells one node from another. Without that context the whole
 * key stays, because a bare `ci.yml` names nothing a reader could act on.
 */
export function leafLabel(node, container) {
    const key = node.key || '';
    // A container the view could not draw a boundary for is back on screen as a point, and its key
    // is synthetic: `org:actions` is not a name anybody uses for that account.
    if (node.kind === 'org') return node.owner || key.replace(/^org:/, '');
    if (!container || !key.startsWith(container)) return key;
    const rest = key.slice(container.length).replace(/^[/@]+/, '');
    if (!rest) return key;
    const leaf = rest.split('/').pop();
    return leaf || rest;
}

/**
 * Where a node lives, as lines under its name.
 *
 * A key alone answers "what is this" and not "whose is it", which is the question a dependency
 * graph exists to answer.
 */
export function nodeDetail(n) {
    const lines = [];
    const kind = (n.kind || '').replace(/_/g, ' ');
    if (kind) lines.push(kind);
    if (n.repository) lines.push(n.repository);
    else if (n.owner) lines.push(n.owner);
    if (n.path && n.path !== n.key) lines.push(n.path);
    if (n.container && n.container !== n.repository) lines.push(`in ${shortKey(n.container)}`);
    if (n.grouping) lines.push(`grouping ${n.grouping}`);
    // Absent is not zero, so an unmeasured node says so rather than reporting none.
    lines.push(n.reachKnown
        ? `${counted(n.dependentRepos, 'repository reaches', 'repositories reach')} it`
        : 'reach not measured');
    if (n.findings) lines.push(counted(n.findings, 'finding', 'findings'));
    return lines;
}

/** A count and the noun it counts, agreeing. "1 repos" is a number the interface did not read. */
export function counted(n, singular, plural) {
    return `${n} ${n === 1 ? singular : plural}`;
}

function shortKey(key) {
    return key.length > 48 ? `\u2026${key.slice(-47)}` : key;
}

/**
 * How solid a halo label is drawn, by how far its node sits from the pointer.
 *
 * Never to nothing: a label that is drawn at all has to stay readable, because an absent label is
 * a rendering budget and a faded one would read as a judgement about the node.
 */
export function haloAlpha(distance, radius) {
    if (!radius) return 1;
    const t = Math.min(1, Math.max(0, distance / radius));
    return 1 - (1 - HALO_MIN_ALPHA) * t;
}

/**
 * The one line of context the canvas keeps.
 *
 * The rest is typeset in the panel beside the graph. Painted here it was the largest box on screen,
 * and it occluded the neighbors a reader hovers a node to compare it against.
 */
export function nodeContext(n) {
    const kind = (n.kind || '').replace(/_/g, ' ');
    const where = [n.repository, n.owner].find(v => v && v !== n.key) || '';
    return [kind, where].filter(Boolean).join(' \u00b7 ');
}

/**
 * What the page is told about a hovered node.
 *
 * Named fields rather than the node itself: the renderer hangs its own mesh and the simulation its
 * velocities off the same object, and serializing that across the interop boundary would send a
 * cycle. Absent measurements travel as their own flag, never as zero.
 */
export function nodeBrief(n) {
    return {
        key: n.key, kind: n.kind || '', origin: n.origin || '',
        repository: n.repository || '', owner: n.owner || '', path: n.path || '',
        container: n.container || '', grouping: n.grouping || '', ref: n.ref || '',
        dependentRepos: n.dependentRepos || 0, reachKnown: !!n.reachKnown,
        findings: n.findings || 0, rules: n.rules || [], facets: n.facets || [],
        advisories: n.advisories || 0,
        refsInUse: n.refsInUse || 0, refsAnalyzed: n.refsAnalyzed || 0,
        discovery: n.discovery || '', depth: n.depth || 0, isExternal: !!n.isExternal,
    };
}

/**
 * The text on a node's label.
 *
 * The key already carries `name@ref` where a dependency is analyzed per reference, so a version
 * names itself. Reach is appended because it was the one thing the tooltip said that the label
 * did not, and losing it when the tooltip went would have been a regression.
 */
export function labelFor(n, container) {
    const name = shortRef(leafLabel(n, container));
    const key = name.length > 60 ? name.slice(0, 59) + '\u2026' : name;
    return n.dependentRepos
        ? `${key} \u00b7 ${counted(n.dependentRepos, 'repository', 'repositories')}`
        : key;
}

/** A commit-pinned reference, short enough to read. The panel carries the whole key. */
export function shortRef(name) {
    return name.replace(/@([0-9a-f]{40})$/, (_, sha) => `@${sha.slice(0, 7)}`);
}

/** Keeps a label box inside the canvas: one centred on a node near the edge falls off it. */
export function clampToCanvas(rect, width) {
    return Math.max(4, Math.min(rect.x, width - rect.w - 4));
}

function drawLabels(it) {
    if (!it.running) return;
    it.drawing = true;

    // The view is only still when the layout has stopped *and* the camera has, because labels
    // are projected and a moving camera changes the frame on its own.
    const cam = cameraSignature(it);
    if (cam !== it.camSig) {
        it.camSig = cam;
        it.settledAt = 0;
    } else if (it.engineStopped && !it.settledAt) {
        it.settledAt = performance.now();
    }

    const level = restLevel(it, performance.now());
    if (level !== 'none') {
        // Coming to rest is what stops an idle tab redrawing an identical frame sixty times a
        // second. `still` has nothing moving at all; `idle` still has particles to advance.
        if (level !== it.resting) {
            it.resting = level;
            if (level === 'still' && it.graph) it.graph.pauseAnimation();
        }
        if (level === 'still') {
            it.drawing = false;
            return;
        }
        setTimeout(() => drawLabels(it), 1000 / REST_FPS);
    } else {
        it.resting = 'none';
        requestAnimationFrame(() => drawLabels(it));
    }

    const p = it.palette;
    const canvas = it.labels;
    if (!p || !canvas || !canvas.isConnected) return;

    const box = it.el.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    if (canvas.width !== Math.round(box.width * dpr)) {
        canvas.width = Math.round(box.width * dpr);
        canvas.height = Math.round(box.height * dpr);
        canvas.style.width = `${box.width}px`;
        canvas.style.height = `${box.height}px`;
    }

    const ctx = canvas.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, box.width, box.height);

    const nodes = it.graph.graphData().nodes;
    if (!nodes.length) return;

    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';

    // Projected once. `labelTiers` decides in screen space, and the old loop did a find() over
    // every node per label, which only stayed cheap while at most a handful were ever drawn.
    // Anything off-canvas is dropped here rather than measured and discarded later.
    const projected = [];
    for (const n of nodes) {
        if (n.x === undefined) continue;
        const pos = it.graph.graph2ScreenCoords(n.x, n.y, n.z || 0);
        if (!pos || pos.x < -40 || pos.y < -40 || pos.x > box.width + 40 || pos.y > box.height + 40) {
            continue;
        }
        projected.push({
            node: n, key: n.key, sx: pos.x, sy: pos.y,
            dependentRepos: n.dependentRepos, reachKnown: n.reachKnown,
            // The fields grouping is read from. Without them `groupOf` falls back to parsing the
            // key, and a container node put back on screen became a group called `org:actions`
            // sitting outside the account it stands for.
            kind: n.kind, repository: n.repository, owner: n.owner,
            grouping: n.grouping, container: n.container,
        });
    }

    const regions = drawRegions(ctx, it, projected, p);
    // `nodeVisibility` governs the scene, not this overlay: a container drawn as a region is still
    // in `graphData`, and labelling it would name a point that is no longer there.
    const labelled = projected.filter(n => !drawnAsRegion(n, it.groupBy, it.collapsed, regions.drawn));

    // Which node the reader is asking about, and the panel that answers. Not the renderer's hit
    // test: it fires on a half-pixel sphere, so the panel sat on one node until a stray pixel
    // happened to land.
    const picked = nearestTo(labelled, it.pointer, PICK_RADIUS);
    const pickedKey = picked ? picked.key : '';
    if (pickedKey !== it.picked) {
        it.picked = pickedKey;
        // Only arrivals: the panel outlives the pointer, so leaving the canvas must not blank it.
        if (picked && it.dotNetRef) {
            it.dotNetRef.invokeMethodAsync('OnNodeHovered', nodeBrief(picked.node));
        }
    }

    const tiers = labelTiers(labelled, {
        hovered: it.picked, selected: it.selected, pointer: it.pointer,
        radius: LABEL_RADIUS, top: LABEL_TOP, haloMax: LABEL_HALO_MAX,
        selectedMax: SELECTED_LABEL_MAX,
    });

    // Which boundary names each node, so its own label can leave that part out. Only regions that
    // carry a label count: one nobody drew a name on supplies no context to lean on.
    const namedBy = new Map();
    for (const s of regions.shapes) {
        if (s.shown < 2) continue;
        for (const m of s.nodes) namedBy.set(m.key, s.key);
    }
    // Detail first, so where two labels want the same pixels the one the reader asked for wins.
    const detail = labelled.filter(p => tiers.get(p.key) === 'detail');
    const named = labelled.filter(p => tiers.get(p.key) === 'name');
    const taken = [];

    // A ring on the node the panel is describing. Without it the panel names something the reader
    // cannot find on screen, which is most of why it read as choosing at random.
    if (picked) {
        ctx.beginPath();
        ctx.arc(picked.sx, picked.sy, 7, 0, Math.PI * 2);
        ctx.strokeStyle = p.focus;
        ctx.lineWidth = 1.5;
        ctx.globalAlpha = 0.9;
        ctx.stroke();
        ctx.globalAlpha = 1;
    }

    // Before any label, so nothing is drawn over the one line that says what is missing.
    drawCaption(ctx, viewCaption(
        it.groupBy && it.groupBy !== 'none' && !it.collapsed
            ? regionCaption(regions.groups, regions.refused, it.groupBy, it.regionNamesUnplaced) : '',
        it.layout, it.pinned), p, taken);
    // Restored after it: a node label is centred on its node, and the note draws left-aligned.
    ctx.textAlign = 'center';

    for (const pt of detail.concat(named)) {
        const isDetail = tiers.get(pt.key) === 'detail';
        const n = pt.node;
        const picked = isSelected(it.selected, n.key);
        // Quiet until asked for: an unprompted name is orientation, and at the size a hovered one
        // wants it was the loudest thing on a canvas whose subject is the shape of the graph.
        const size = isDetail || picked ? 13 : 11;
        // One line on the canvas; everything else is typeset in the panel beside the graph, where
        // it occludes none of the neighbors a reader hovers a node to compare it against.
        const lines = isDetail ? [nodeContext(n)].filter(Boolean) : [];

        ctx.font = `${picked || isDetail ? '600 ' : ''}${size}px system-ui, sans-serif`;
        const label = labelFor(n, namedBy.get(n.key));
        let w = ctx.measureText(label).width;
        ctx.font = `${size - 2}px system-ui, sans-serif`;
        for (const l of lines) w = Math.max(w, ctx.measureText(l).width);

        const h = size + (size - 1) * lines.length;
        const top = pt.sy + nodeRadius(n, it.selected) + 6;
        const rect = { x: pt.sx - w / 2 - 5, y: top - 4, w: w + 10, h: h + 8 };
        rect.x = clampToCanvas(rect, box.width);
        const cx = rect.x + rect.w / 2;

        // Only the node under the pointer is guaranteed its pixels. "Anything in the detail tier"
        // was the same rule until a selection could be one dependency drawn as many nodes, which
        // sat them on one spot and stacked their labels into the pile this exemption exists to
        // prevent. The rest stay visibly selected, which is what says they are in the set.
        if (pt.key !== it.picked && taken.some(t => overlaps(t, rect))) continue;
        taken.push(rect);

        // A halo label fades with its distance from the pointer, so the patch reads as one gesture
        // rather than as six equally loud claims.
        const fade = !isDetail && it.pointer
            ? haloAlpha(Math.hypot(pt.sx - it.pointer.x, pt.sy - it.pointer.y), LABEL_RADIUS)
            : 1;

        ctx.fillStyle = p.background;
        ctx.globalAlpha = (isDetail ? 0.97 : 0.88) * fade;
        ctx.beginPath();
        if (ctx.roundRect) ctx.roundRect(rect.x, rect.y, rect.w, rect.h, 4);
        else ctx.rect(rect.x, rect.y, rect.w, rect.h);
        ctx.fill();
        ctx.globalAlpha = fade;

        ctx.font = `${picked || isDetail ? '600 ' : ''}${size}px system-ui, sans-serif`;
        ctx.fillStyle = picked ? p.focus : p.label;
        ctx.fillText(label, cx, top + size / 2);

        ctx.font = `${size - 2}px system-ui, sans-serif`;
        ctx.fillStyle = p.muted || p.label;
        lines.forEach((l, i) => ctx.fillText(l, cx, top + size + (size - 1) * i + (size - 1) / 2));
        ctx.globalAlpha = 1;
    }

    // Last, so a boundary's name is never buried under the labels of what it contains, and sharing
    // the same collision list so the two kinds of label stop stacking on each other.
    settleUnplacedNames(it, labelRegions(ctx, regions.shapes, p, taken, it.pointer,
        { width: box.width, height: box.height }));
    settleRegionKeys(it, regions.drawn);
}

// The caption is drawn before the names are placed, so a count taken during placement can only
// reach it on the next frame. Waking on a change is what makes that frame happen -- otherwise a
// view that came to rest would keep the count from the frame before its last one.
function settleUnplacedNames(it, unplaced) {
    if (unplaced === it.regionNamesUnplaced) return;
    it.regionNamesUnplaced = unplaced;
    wake(it);
}

// Which groups a boundary is standing for, so the renderer can put back the container node of one
// it refused. Re-applied only when the set changes: at rest it does not, and a refresh per frame
// would rebuild every node object sixty times a second.
function settleRegionKeys(it, drawn) {
    const sig = [...drawn].sort().join('\u0000');
    if (sig === it.regionSig) return;
    it.regionSig = sig;
    it.regionKeys = drawn;
    if (it.graph) it.graph.refresh();
}

// Top left, because the renderer writes its own control hint along the bottom, and reserved in the
// collision list so no label is drawn over the one line that says how the view is grouped.
function drawCaption(ctx, text, p, taken) {
    if (!text) return;
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    ctx.font = '11px system-ui, sans-serif';
    const rect = { x: 4, y: 5, w: ctx.measureText(text).width + 12, h: 18 };
    taken.push(rect);

    ctx.fillStyle = p.background;
    ctx.globalAlpha = 0.8;
    ctx.beginPath();
    if (ctx.roundRect) ctx.roundRect(rect.x, rect.y, rect.w, rect.h, 3);
    else ctx.rect(rect.x, rect.y, rect.w, rect.h);
    ctx.fill();
    ctx.globalAlpha = 1;
    ctx.fillStyle = p.muted || p.label;
    ctx.fillText(text, 10, rect.y + rect.h / 2);
}

/** Two label boxes sharing pixels. */
function overlaps(a, b) {
    return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

/**
 * The boundaries a grouping produces over already-projected nodes, with no canvas involved.
 *
 * A boundary that would enclose a node belonging to somebody else is **refused**, not redrawn: a
 * region asserts containment, and a hull that swallows a stranger states a relationship that does
 * not exist. Splitting such a group at its widest gap was tried and measured worse on the real
 * corpus — 23 boundaries became 82, nodes inside a foreign boundary went 71 to 197 and pairwise
 * overlap 21% to 533%, because at a median nearest-neighbour spacing of 2px every fragment
 * encloses its neighbors too.
 *
 * Separate from the painting so those claims can be checked without a renderer.
 *
 * `boxes` are the rectangles a layout engine reserved for each group. Where there is one it *is*
 * the boundary, because re-deriving a hull would draw a shape the engine did not place. The
 * refusal above still applies to it: `dot` separates the clusters of the graph it was handed, and
 * a browser filter drawing a subset of that graph is a different picture.
 */
export function regionShapes(projected, by, totals = new Map(), floor = REGION_FLOOR, boxes = null) {
    if (!by || by === 'none') return { shapes: [], groups: 0, refused: 0 };

    const byX = [...projected].sort((a, b) => a.sx - b.sx);
    const shapes = [];
    let groups = 0, refused = 0;
    for (const [key, group] of regionMembers(projected, by)) {
        groups++;
        const padding = regionPadding(group, floor, REGION_CEILING);
        const box = boxes && boxes.get(key);
        const hull = box ? boxHull(box) : inflate(hullOf(group), padding);
        if (hull.length < 3) continue;
        const members = new Set(group);
        // An engine box is already clear of its own members, so the neighbour sweep only needs to
        // reach as far as the box itself.
        const reach = box ? 0 : padding;
        if (foreignNear(byX, members, reach).some(f => containsPoint(hull, { x: f.sx, y: f.sy }))) {
            refused++;
            continue;
        }
        shapes.push({
            key, hull, area: hullArea(hull), members: group.length, nodes: group,
            shown: group.length, total: totals.get(key) || group.length, named: true,
        });
    }
    // Widest first, so a small region sitting inside a large one is drawn on top of it rather than
    // under it, where its boundary would be the one nobody can see.
    shapes.sort((a, b) => b.area - a.area);
    return { shapes, groups, refused };
}

/**
 * The one line that says what the boundaries are, and what is missing from them.
 *
 * The level used to be repeated on every boundary — seventeen copies of the word `owner` on a
 * 669px canvas, which is where the names got wide enough to stack into a column. `github` alone
 * still does not say whether it is an account or a business unit, and the control that decided it
 * is in a toolbar nobody is looking at, so the level is stated once, here.
 *
 * The refusal rides on the same line because a withheld boundary a reader cannot see is worse than
 * the boundary itself: the control claims to be grouping and the canvas shows less than there is.
 */
export function regionCaption(groups, refused, by, unnamed = 0) {
    const level = LEVEL_WORD[by] || by;
    let line = `grouped by ${level}`;
    if (refused) {
        line += ` \u00b7 ${refused} of ${groups} interleave with other groups and are not drawn`;
    }
    // A different fact from a refusal: the boundary is there and correct, and the canvas ran out
    // of room to say whose it is. Counting shapes and names and finding different numbers, with
    // nothing explaining the gap, is the same defect as an undisclosed refusal.
    if (unnamed) {
        line += unnamed === 1
            ? ' \u00b7 1 has no room for its name'
            : ` \u00b7 ${unnamed} have no room for their names`;
    }
    return line;
}

/**
 * The one line on the canvas, which is the grouping and what drew the picture.
 *
 * The simulation is the default and says nothing extra: a note on every ordinary view is clutter
 * on the line reserved for what is missing. A layout somebody else computed is the claim that
 * needs stating — nothing else on the canvas tells a reader whether what they are looking at will
 * be the same tomorrow, and a partial answer has to carry its own share.
 */
export function viewCaption(grouped, layout, pins) {
    if (!layout) return grouped || '';
    const summary = layoutSummary(layout, pins || {});
    return [grouped, summary.text].filter(Boolean).join(' \u00b7 ');
}

/**
 * The boundaries a view settles on, and which groups they stand for.
 *
 * The answer depends on itself: a refused boundary puts its container back on screen as a point,
 * and that point is something the next boundary has to clear. Deciding it one frame at a time left
 * a view that came to rest before it had finished — 9 boundaries drawn where 17 were drawable, with
 * nothing scheduled to wake it.
 *
 * Shapes are only ever *dropped*, never added back, so this terminates in at most one round per
 * group. That matters more than speed: an iteration that gave up after a fixed number of passes
 * returned a shape set inconsistent with the containers it then painted, and two nodes ended up
 * inside a boundary they do not belong to.
 */
export function settledRegions(projected, by, totals = new Map(), floor = REGION_FLOOR, boxes = null) {
    const all = regionShapes(projected, by, totals, floor, boxes);
    let shapes = all.shapes;

    for (;;) {
        const drawn = new Set(shapes.map(s => s.key));
        const painted = projected.filter(n => !drawnAsRegion(n, by, false, drawn));
        const safe = shapes.filter(s => {
            const own = new Set(s.nodes);
            return !painted.some(p => !own.has(p) && containsPoint(s.hull, { x: p.sx, y: p.sy }));
        });
        if (safe.length === shapes.length) {
            return { shapes, groups: all.groups, refused: all.groups - shapes.length, drawn };
        }
        shapes = safe;
    }
}

/**
 * Whether a press and release were a click rather than a drag.
 *
 * A drag that ends over the canvas still fires a `click`, so only the distance travelled tells
 * them apart. force-graph has its own answer — `clickAfterDrag` defaults to false — but it is a
 * heuristic we cannot see, tune or test, and it silently dropped clicks that carried a couple of
 * pixels of trackpad drift.
 */
export function isClick(down, up, slop) {
    if (!down || !up) return false;
    return Math.hypot(up.x - down.x, up.y - down.y) < slop;
}

/**
 * What a click on the canvas means.
 *
 * A single click marks; a second on the same node within the gap narrows to it. Rebuilding the
 * view is a large enough change that it takes a deliberate second action, and a different node is
 * a new question however fast the reader moved.
 */
export function clickAction(key, last, now, gap = 350) {
    if (!key) return 'clear';
    return last && last.key === key && now - last.at < gap ? 'isolate' : 'select';
}

/** Applies that decision, from whichever path reported the click. */
function canvasClick(it, key, event, dotNetRef) {
    const now = performance.now();
    const action = clickAction(key, { key: it.lastClickKey, at: it.lastClickAt }, now);
    it.lastClickKey = key;
    it.lastClickAt = now;

    if (action === 'isolate') {
        if (dotNetRef) dotNetRef.invokeMethodAsync('OnNodeIsolated', key);
        return;
    }
    // Cmd on macOS, Ctrl elsewhere, toggles; Shift adds without removing.
    const toggle = !!(event && (event.metaKey || event.ctrlKey));
    const add = !!(event && event.shiftKey);
    selectNode(it, key, { toggle, add });
    if (dotNetRef) dotNetRef.invokeMethodAsync('OnNodeSelected', [...it.selected]);
}

/**
 * The node a pointer is asking about.
 *
 * Nearest in screen space rather than whatever the renderer's raycaster hit: a node is drawn at
 * about half a pixel at the default scope, so the hit test almost never fires and the panel read
 * as stuck on whichever node it last managed to catch. The label pass has already projected every
 * node, so this costs one comparison each.
 *
 * Ties break on the key so the answer does not depend on iteration order, which would make the
 * panel flicker between two nodes the same distance away.
 */
export function nearestTo(points, pointer, radius) {
    if (!pointer) return null;
    let best = null;
    let bestD = radius * radius;
    for (const p of points) {
        const dx = p.sx - pointer.x, dy = p.sy - pointer.y;
        const d = dx * dx + dy * dy;
        if (d < bestD || (best && d === bestD && p.key < best.key)) {
            best = p;
            bestD = d;
        }
    }
    return best;
}

/**
 * A container whose children are visible, drawn as the boundary around them.
 *
 * Under the node labels rather than over them, and mostly transparent: the overlay sits above the
 * WebGL surface, so a solid fill would veil the very nodes the boundary exists to gather. The
 * stroke carries the meaning and the fill only hints at extent.
 */
function drawRegions(ctx, it, projected, p) {
    if (!it.groupBy || it.groupBy === 'none' || it.collapsed) {
        return { shapes: [], groups: 0, refused: 0, drawn: new Set() };
    }

    const totals = groupSizes(it.all ? it.all.nodes : [], it.groupBy);
    const result = settledRegions(projected, it.groupBy, totals, undefined, it.layoutBoxes);
    const focused = hoveredRegion(result.shapes, it.pointer);

    for (const s of result.shapes) {
        const ink = regionInk(s.key, isDarkBackground(p.background));
        roundedPath(ctx, s.hull, 10);

        // The fill is the only thing that changes under the pointer. A thicker line reads as a
        // different kind of boundary rather than the same one being pointed at.
        ctx.fillStyle = ink.fill;
        ctx.globalAlpha = s === focused ? 0.11 : 0.05;
        ctx.fill();
        ctx.globalAlpha = 0.5;
        ctx.strokeStyle = ink.stroke;
        ctx.lineWidth = 1.25;
        ctx.stroke();
        ctx.globalAlpha = 1;
    }
    return result;
}

// Only the nodes whose x could fall inside the group's own span, so testing every boundary against
// every node does not become the cost of a frame on a graph with hundreds of groups.
function foreignNear(sortedByX, members, pad) {
    let lo = Infinity, hi = -Infinity;
    for (const m of members) {
        if (m.sx < lo) lo = m.sx;
        if (m.sx > hi) hi = m.sx;
    }
    lo -= pad;
    hi += pad;

    let start = 0, end = sortedByX.length;
    while (start < end) {
        const mid = (start + end) >> 1;
        if (sortedByX[mid].sx < lo) start = mid + 1;
        else end = mid;
    }

    const out = [];
    for (let i = start; i < sortedByX.length && sortedByX[i].sx <= hi; i++) {
        if (!members.has(sortedByX[i])) out.push(sortedByX[i]);
    }
    return out;
}

// A stable hue per group, so two adjacent boundaries are told apart without a legend and the same
// account keeps its color between renders.
function regionHue(key) {
    let h = 2166136261;
    for (let i = 0; i < key.length; i++) {
        h ^= key.charCodeAt(i);
        h = Math.imul(h, 16777619);
    }
    return (h >>> 0) % 360;
}

/**
 * Whether the scheme in force is the dark one, read from the background it paints on.
 *
 * The palette carries no flag and the answer is a property of that color anyway. A hue tuned to
 * read on near-black washes out to nothing on white, which is where the region names went.
 */
export function isDarkBackground(color) {
    const hex = /^#([0-9a-f]{6})$/i.exec(color || '');
    const rgb = hex
        ? [0, 2, 4].map(i => parseInt(hex[1].slice(i, i + 2), 16))
        : (color || '').match(/\d+(\.\d+)?/g)?.slice(0, 3).map(Number);
    if (!rgb || rgb.length < 3) return true;
    return (0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]) < 128;
}

/** A boundary's stroke and its name, at a lightness the scheme can actually show. */
function regionInk(key, dark) {
    const hue = regionHue(key);
    return dark
        ? { fill: `hsl(${hue} 70% 60%)`, stroke: `hsl(${hue} 65% 62%)`, name: `hsl(${hue} 60% 72%)` }
        : { fill: `hsl(${hue} 70% 45%)`, stroke: `hsl(${hue} 60% 42%)`, name: `hsl(${hue} 70% 30%)` };
}

// Corners are rounded because a boundary is a region rather than a diagram: the polygon's vertices
// are wherever a member happened to land, and a sharp one draws the eye to an accident.
function roundedPath(ctx, hull, radius) {
    ctx.beginPath();
    for (let i = 0; i < hull.length; i++) {
        const prev = hull[(i - 1 + hull.length) % hull.length];
        const cur = hull[i];
        const next = hull[(i + 1) % hull.length];
        const toPrev = trim(cur, prev, radius);
        const toNext = trim(cur, next, radius);
        if (i === 0) ctx.moveTo(toPrev.x, toPrev.y);
        else ctx.lineTo(toPrev.x, toPrev.y);
        ctx.quadraticCurveTo(cur.x, cur.y, toNext.x, toNext.y);
    }
    ctx.closePath();
}

function trim(from, toward, radius) {
    const dx = toward.x - from.x, dy = toward.y - from.y;
    const len = Math.hypot(dx, dy) || 1;
    const d = Math.min(radius, len / 2);
    return { x: from.x + (dx / len) * d, y: from.y + (dy / len) * d };
}

// What the boundaries at each level enclose, said once in the caption. `github` alone does not
// tell a reader whether it is an account, a repository or a business unit, and the control that
// decided it is in a toolbar they are not looking at.
const LEVEL_WORD = {
    owner: 'owner',
    repo: 'repository',
    workflow: 'workflow',
    grouping: 'grouping',
};

/**
 * A boundary's name, in the two pieces it is styled in.
 *
 * Compact at rest, because seventeen of these share a 669px canvas and the long form is what made
 * them stack into a column — and full when the pointer is inside the boundary, which is the one
 * the reader is asking about. The count discloses its own denominator either way whenever the two
 * differ, because a boundary drawn around the members that survived a filter otherwise implies the
 * container is only that.
 */
export function regionLabelParts(key, shown, total, detailed) {
    if (detailed) {
        return { name: key, count: total > shown ? ` \u00b7 ${shown} of ${total}` : ` \u00b7 ${shown}` };
    }
    // A complete boundary already shows all its members, so its size is not news.
    return { name: key, count: total > shown ? ` ${shown}/${total}` : '' };
}

/** The boundary the pointer is in, smallest first: nested and overlapping boundaries both happen. */
export function hoveredRegion(shapes, pointer) {
    if (!pointer) return null;
    let best = null;
    for (const s of shapes) {
        if (best && s.area >= best.area) continue;
        if (containsPoint(s.hull, pointer)) best = s;
    }
    return best;
}

/**
 * Where a boundary's name goes: inside it if it fits, otherwise outside with a line back to it,
 * and never off the canvas.
 *
 * Names used to be dropped outright on a collision, so in the crowded middle the regions that most
 * needed naming were the ones that went unnamed — and a name placed above a distant boundary is a
 * name whose region a reader cannot identify. Placement was then unaware of the canvas, which put
 * four of 24 names over its edge on the real corpus, `github` and `has-ghas` among them: the
 * largest account and the operator's own, drawn where neither could be read.
 */
export function regionLabelSpot(hull, width, height, taken, canvas) {
    if (!hull || hull.length < 3) return null;
    let cx = 0, topY = Infinity, botY = -Infinity, leftX = Infinity, rightX = -Infinity;
    for (const v of hull) {
        cx += v.x;
        if (v.y < topY) topY = v.y;
        if (v.y > botY) botY = v.y;
        if (v.x < leftX) leftX = v.x;
        if (v.x > rightX) rightX = v.x;
    }
    cx /= hull.length;
    const midY = (topY + botY) / 2;

    // Clamped before the collision test rather than after it, or two names pushed off the same
    // edge are compared where they were going to be and drawn where they end up.
    const onCanvas = (x, y) => {
        if (!canvas) return { x, y };
        const dx = width / 2, dy = height / 2;
        return {
            x: canvas.width >= width ? Math.min(Math.max(x, dx), canvas.width - dx) : x,
            y: canvas.height >= height ? Math.min(Math.max(y, dy), canvas.height - dy) : y,
        };
    };
    const clear = at => !taken.some(t => overlaps(t, {
        x: at.x - width / 2, y: at.y - height / 2, w: width, h: height,
    }));
    const fits = at => {
        const dx = width / 2, dy = height / 2;
        return [[-dx, -dy], [dx, -dy], [-dx, dy], [dx, dy]]
            .every(([ox, oy]) => containsPoint(hull, { x: at.x + ox, y: at.y + oy }));
    };

    const step = height * 1.4;
    for (let i = 0; i < 4; i++) {
        const at = onCanvas(cx, topY + height / 2 + 4 + i * step);
        if (fits(at) && clear(at)) return { x: at.x, y: at.y, inside: true };
    }

    // Above first, then below, then to either side. Walking upwards alone was enough until the
    // clamp went in: against the top edge every rung lands on the same clamped row, so one
    // blocked position blocked all of them and the boundary was drawn with no name.
    const outside = [];
    for (let i = 0; i < 6; i++) outside.push({ x: cx, y: topY - height / 2 - 6 - i * step });
    for (let i = 0; i < 4; i++) outside.push({ x: cx, y: botY + height / 2 + 6 + i * step });
    for (let i = 0; i < 3; i++) {
        const gap = width / 2 + 10 + i * step;
        outside.push({ x: rightX + gap, y: midY });
        outside.push({ x: leftX - gap, y: midY });
    }
    // The anchor is the region's own edge and is never clamped: moving it with the name would
    // point the line at wherever the name landed rather than at what it names.
    for (const want of outside) {
        const at = onCanvas(want.x, want.y);
        if (clear(at)) {
            return { x: at.x, y: at.y, inside: false, leader: { x: cx, y: want.y < midY ? topY : botY } };
        }
    }
    return null;
}

/**
 * The boundary's own name, which is what replaces a label on every node inside it.
 *
 * Returns how many it could not place. A boundary drawn with no name is the one thing this view
 * may not do silently: the reader is looking at a shape with no way to tell whose it is.
 */
function labelRegions(ctx, shapes, p, taken, pointer, canvas) {
    let unplaced = 0;
    const focused = hoveredRegion(shapes, pointer);
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';

    // Widest last, so a large boundary's name is placed after the small ones it contains have
    // claimed their room rather than over them.
    for (const s of [...shapes].reverse()) {
        // A group of one is its member. Naming the boundary as well says the same thing twice, and
        // at owner level most accounts have exactly one node.
        if (!s.named || s.shown < 2) continue;

        const detailed = s === focused;
        const size = detailed ? 12 : 10;
        const parts = regionLabelParts(s.key, s.shown, s.total, detailed);
        const small = `${size - 2}px system-ui, sans-serif`;
        const bold = `600 ${size}px system-ui, sans-serif`;
        ctx.font = small;
        const countW = ctx.measureText(parts.count).width;
        ctx.font = bold;
        const nameW = ctx.measureText(parts.name).width;
        const w = nameW + countW;
        const h = size + 6;

        const spot = regionLabelSpot(s.hull, w + 12, h, taken, canvas);
        if (!spot) {
            unplaced++;
            continue;
        }
        const rect = { x: spot.x - (w + 12) / 2, y: spot.y - h / 2, w: w + 12, h };
        taken.push(rect);

        const ink = regionInk(s.key, isDarkBackground(p.background));
        // A name that had to sit outside its boundary is a name whose region is unknown without
        // one, because the nearest boundary to it is usually somebody else's.
        if (spot.leader) {
            ctx.strokeStyle = ink.stroke;
            ctx.globalAlpha = 0.55;
            ctx.lineWidth = 1;
            ctx.beginPath();
            ctx.moveTo(spot.x, rect.y + h);
            ctx.lineTo(spot.leader.x, spot.leader.y);
            ctx.stroke();
            ctx.globalAlpha = 1;
        }

        ctx.fillStyle = p.background;
        ctx.globalAlpha = detailed ? 0.92 : 0.7;
        ctx.beginPath();
        if (ctx.roundRect) ctx.roundRect(rect.x, rect.y, rect.w, rect.h, 3);
        else ctx.rect(rect.x, rect.y, rect.w, rect.h);
        ctx.fill();
        ctx.globalAlpha = 1;

        const left = spot.x - w / 2;
        ctx.font = bold;
        ctx.fillStyle = ink.name;
        ctx.fillText(parts.name, left, spot.y);
        ctx.font = small;
        ctx.fillStyle = p.muted || p.label;
        ctx.fillText(parts.count, left + nameW, spot.y);
    }
    return unplaced;
}

/**
 * Where a node starts before any force has acted on it.
 *
 * On an ellipse shaped like the canvas rather than a circle. Weakening gravity across the wide
 * axis alone moved the cloud from 45% of the canvas width to 57%, because the simulation stops
 * after a fixed number of ticks and had not finished travelling: a reheat took it further again.
 * Starting at roughly the right shape means the relaxation refines it instead of reaching for it.
 *
 * Deterministic in the key, so the same corpus lays out identically on every visit.
 */
export function seedAt(key, spread, aspect, use3d) {
    const angle = seed(key) * Math.PI * 2;
    const r = spread * (0.35 + seed(`${key}#r`) * 0.65);
    return {
        x: Math.cos(angle) * r * Math.max(aspect || 1, 1),
        y: Math.sin(angle) * r,
        // Not `* 0` in 2D: that yields -0 for half the corpus, which compares unequal to 0.
        z: use3d ? (seed(`${key}#z`) - 0.5) * spread : 0,
    };
}

/**
 * The dense part of the graph, as a box centred on itself.
 *
 * A handful of stragglers sit far outside everything else, and framing them shrinks the part worth
 * looking at to a few pixels while leaving zoom nowhere useful to go. The percentile is taken from
 * the centroid rather than the origin, because a cloud the layout has drifted off centre is not an
 * outlier problem and must not be framed as one.
 */
export function coreBounds(points, percentile) {
    const n = points.length;
    if (!n) return { cx: 0, cy: 0, cz: 0, halfWidth: 0, halfHeight: 0 };

    let sx = 0, sy = 0, sz = 0;
    for (const p of points) {
        sx += p.x; sy += p.y; sz += p.z || 0;
    }
    const mx = sx / n, my = sy / n, mz = sz / n;

    const withRadius = points
        .map(p => ({ p, r: Math.hypot(p.x - mx, p.y - my, (p.z || 0) - mz) }))
        .sort((a, b) => a.r - b.r);
    const keep = withRadius.slice(0, Math.max(1, Math.ceil(n * (percentile || 0.9))));

    let lo = Infinity, hi = -Infinity, top = Infinity, bottom = -Infinity, near = Infinity, far = -Infinity;
    for (const { p } of keep) {
        lo = Math.min(lo, p.x); hi = Math.max(hi, p.x);
        top = Math.min(top, p.y); bottom = Math.max(bottom, p.y);
        near = Math.min(near, p.z || 0); far = Math.max(far, p.z || 0);
    }
    return {
        cx: (lo + hi) / 2, cy: (top + bottom) / 2, cz: (near + far) / 2,
        halfWidth: Math.max(1, (hi - lo) / 2), halfHeight: Math.max(1, (bottom - top) / 2),
    };
}

/**
 * How far back the camera has to sit for a box to fit a canvas of this shape.
 *
 * The old fit framed a sphere, so on a 1.86:1 canvas a tall cloud was cut off the bottom with half
 * the width unused — the nodes spanned x 216–516 of 669 and y 46–426 of 360.
 */
export function fitDistance(halfWidth, halfHeight, aspect, fov) {
    const half = Math.tan((fov * Math.PI / 180) / 2);
    return Math.max(halfHeight, halfWidth / Math.max(aspect, 0.01)) / half;
}

/**
 * How much further back the camera has to sit to leave `gutter` screen pixels around the core.
 *
 * Stated in pixels because that is what the thing needing the room is measured in: the boundary is
 * inflated by a fixed 26px however far away the camera is, and so is its name.
 */
export function fitMargin(gutter, halfHeightPx) {
    if (!(halfHeightPx > 0)) return 1;
    return Math.min(2, (halfHeightPx + gutter) / halfHeightPx);
}

/** Frames the dense part of the graph in the room the canvas actually has. */
function fitCore(it, percentile) {
    const nodes = it.graph.graphData().nodes.filter(n => n.x !== undefined);
    if (!nodes.length) return;

    const box = it.el.getBoundingClientRect();
    const aspect = box.height > 0 ? box.width / box.height : 1;
    const core = coreBounds(nodes, percentile || 0.9);
    const camera = it.graph.camera();
    const fov = (camera && camera.fov) || 50;
    const margin = fitMargin(FIT_GUTTER, box.height / 2);
    const z = Math.max(60, fitDistance(core.halfWidth, core.halfHeight, aspect, fov) * margin);

    it.graph.cameraPosition(
        { x: core.cx, y: core.cy, z: core.cz + z },
        { x: core.cx, y: core.cy, z: core.cz },
        600);
}

/**
 * Which mouse button does what.
 *
 * Trackball hardcodes the action per case and matches on the *button number*, so there is no way
 * to say "left pans" other than to swap the two buttons. A flat view has nothing to rotate, which
 * left the button a reader drags first doing nothing while the canvas hint promised it panned:
 * measured on a settled 2D view, a left drag moved the camera **0** units and a right drag 1,831.
 */
export function buttonMap(buttons, use3d) {
    return use3d ? { ...buttons } : { ...buttons, LEFT: buttons.RIGHT, RIGHT: buttons.LEFT };
}

/** Whether motion is shown at all: the settling layout, and the particles along the selection. */
export function setAnimate(elementId, animate) {
    const it = instances.get(elementId);
    if (!it) return;
    it.animate = animate !== false;
    applySettleBudget(it);
    startParticleCycle(it);
    if (it.palette) {
        it.graph.linkDirectionalParticles(l => particleCount(it, l));
    }
    wake(it);
}

/** Re-measures the host, for when something beside it changed size. */
export function resize(elementId) {
    const it = instances.get(elementId);
    if (it) fitHost(it.el);
}

export function setPalette(elementId, palette) {
    const it = instances.get(elementId);
    if (!it) return;
    it.palette = palette;
    paint(it);
    wake(it);
}

/**
 * Rebuilds node meshes after a color change.
 *
 * A custom shape holds its color in the material it was created with, so repainting the
 * accessor is not enough; the objects themselves have to be discarded and remade.
 */
export function rebuildNodes(elementId) {
    const it = instances.get(elementId);
    if (!it || !it.shapes) return;
    paint(it);
    it.graph.nodeThreeObject(it.graph.nodeThreeObject());
}

export function setView(elementId, view) {
    const it = instances.get(elementId);
    if (!it) return;
    const recolors = it.view === 'frontier' || view === 'frontier';
    it.view = view;
    applyForces(it);
    paint(it);
    // Coverage answers with hue, not position, and a custom shape holds its color in the
    // material it was built with. Switching into or out of it has to remake the meshes.
    if (recolors && it.shapes) {
        it.graph.nodeThreeObject(it.graph.nodeThreeObject());
    }
    wake(it);
    it.graph.d3ReheatSimulation();
}

export function setPriorities(elementId, priorities) {
    const it = instances.get(elementId);
    if (!it || !it.all) return;
    for (const n of it.all.nodes) {
        n.priority = priorities[n.key] || 0;
    }
    // The rendered node set does not change on a view switch, so nothing else recomputes this
    // and the force would divide by whatever the last rebuild left behind.
    it.maxPriority = Math.max(1, ...it.all.nodes.map(n => n.priority || 0));
}






/** Chooses what nodes are pulled together by, or `none` to leave the layout alone. */
export function setGroupBy(elementId, by) {
    const it = instances.get(elementId);
    if (!it || it.groupBy === by) return;
    it.groupBy = by || 'none';
    // Which boundaries can be drawn is a fact about the level that just changed, so the old answer
    // would hide containers belonging to a grouping nobody is looking at any more.
    it.regionSig = undefined;
    it.regionKeys = null;
    // The engine's boxes were computed for the previous level. Re-keyed they would draw a boundary
    // around members nobody grouped together, so they are dropped until the caller asks again.
    it.layoutBoxes = null;
    applyForces(it);
    wake(it);
    it.graph.d3ReheatSimulation();
}

/**
 * Folds each group into one node, or unfolds them again.
 *
 * Members keep their coordinates while collapsed, so expanding returns them where they were
 * rather than re-settling a layout the reader had already made sense of.
 */
export function setCollapsed(elementId, collapsed, spec) {
    const it = instances.get(elementId);
    if (!it || it.collapsed === !!collapsed) return;
    it.collapsed = !!collapsed;
    it.seeded = false;
    return applyFilters(elementId, spec);
}

/**
 * Draws the graph where an engine placed it, rather than where the simulation settles.
 *
 * A failure is not thrown: the picture falls back to the simulation and the summary carries the
 * reason, because a canvas that went blank when Graphviz was absent would make an optional tool
 * look like a broken page. The server says the same thing for its own part, answering
 * `producedBy: "none"` rather than a fault.
 */
export async function applyServerLayout(elementId, url) {
    const it = instances.get(elementId);
    if (!it) return null;

    let data;
    try {
        const res = await fetch(url);
        if (!res.ok) {
            const body = await res.text().catch(() => '');
            const drawn = it.graph ? it.graph.graphData().nodes.length : 0;
            return summarise(it, null, refusalText(body, res.status, drawn));
        }
        data = await res.json();
    } catch (err) {
        return summarise(it, null, `layout request failed: ${err.message}`);
    }
    if (data.producedBy !== 'graphviz') return summarise(it, null, data.reason);

    it.layout = data;
    applyLayoutFrame(it);
    it.regionSig = undefined;
    settleView(it);
    return { ...layoutSummary(data, it.pinned), ...it.pinned };
}

/**
 * Fits the held layout to the nodes currently on screen, and pins them.
 *
 * The layout endpoint answers from server terms only, so it routinely places more than the browser
 * draws. Framing the canvas on the engine's own extent would size the view for nodes that are not
 * there, so the fit is taken from the drawn set and recomputed whenever that set changes.
 */
function applyLayoutFrame(it) {
    if (!it.layout || !it.graph) return;
    const nodes = it.graph.graphData().nodes;
    const drawn = new Set(nodes.map(n => n.key));
    it.placed = fitPositions(it.layout, { drawn });
    // Boxes belong to the grouping that was asked for. Keyed by any other level they would put a
    // boundary around members nobody grouped together, so an engine that drew none leaves this
    // empty rather than stale.
    it.layoutBoxes = it.layout.groupingDrawn === false
        ? null
        : clusterBoxes(it.layout, it.groupBy, { drawn });
    it.pinned = applyPositions(nodes, it.placed);
}

/** Hands the view back to the simulation, which is the default and the only other answer. */
export function clearServerLayout(elementId) {
    const it = instances.get(elementId);
    if (!it || !it.layout) return;
    releaseLayout(it);
    settleView(it);
    it.graph.d3ReheatSimulation();
}

/** Drops every trace of a server layout, so nothing downstream reads half of one. */
function releaseLayout(it) {
    releaseAll(it.graph ? it.graph.graphData().nodes : []);
    it.layout = null;
    it.placed = null;
    it.layoutBoxes = null;
    it.pinned = null;
    it.regionSig = undefined;
}

/** The summary for a picture the engine did not draw, with whatever it is still holding released. */
function summarise(it, layout, reason) {
    const held = !!it.layout;
    releaseLayout(it);
    if (held) settleView(it);
    return { ...layoutSummary(layout || { producedBy: 'none', reason }, {}), pinned: 0, free: 0 };
}

/** Repaints and re-fits after the positions under the picture have been replaced. */
function settleView(it) {
    wake(it);
    if (it.graph) {
        it.graph.refresh();
        fitCore(it);
    }
}



/**
 * The set a node's key is tested against before it is drawn, or null to draw what was rendered.
 *
 * A fold is built from the already-filtered nodes, so every group in it is visible by
 * construction. Gating it on `it.visible` compares group keys with the member keys they replaced,
 * matches none of them, and hides the entire collapsed view while the header still counts it.
 */
export function visibilityGate(it) {
    return it.collapsed ? null : it.visible;
}

/**
 * The node kind that *is* a grouping level, and so is drawn as a region rather than as a point.
 *
 * A repository grouping has none: it is a property of a repository, not a node in the graph, so at
 * that level nothing is suppressed and the region is pure addition.
 */
export function containerKindFor(by) {
    switch (by) {
        case 'owner': return 'org';
        case 'repo': return 'repo';
        case 'workflow': return 'workflow';
        default: return null;
    }
}

/**
 * Whether a node is the container the current view draws as a boundary.
 *
 * An account is not a peer of the repositories inside it, and drawing it as one more point among
 * them is what made every unprompted label read `org:` while the reader was grouping by something
 * else entirely. Collapsed is the other rendering of the same fold and still wants the point.
 */
export function drawnAsRegion(node, by, collapsed, drawn) {
    if (collapsed) return false;
    const kind = containerKindFor(by);
    if (!kind || node.kind !== kind) return false;
    // A boundary that could not be drawn honestly leaves the container as the only thing standing
    // for the group. `null` is "not measured yet", which is the first frame.
    return !drawn || drawn.has(groupOf(node, by));
}

/** An edge whose whole content is containment the boundary now states. */
export function containmentIntoRegion(link, by, collapsed, drawn) {
    if (!containerKindFor(by) || collapsed) return false;
    const source = typeof link.source === 'object' ? link.source : null;
    return !!source && drawnAsRegion(source, by, collapsed, drawn);
}

function paint(it) {
    const p = it.palette;
    if (!p) return;

    const visible = visibilityGate(it);
    const em = it.emphasis;
    const lit = l => !em || em.has(keyOf(l.source)) || em.has(keyOf(l.target));
    // An edge with a context end leads somewhere the reader did not ask about. Grey rather than
    // gone: removing it is what left 39 of 7,552 relationships on screen. 0.10 rather than the
    // 0.22 that looked right on one edge -- several thousand overlap in the middle and summed to
    // a white haze that swallowed the findings they were drawn around.
    const ctx = it.contextKeys || new Set();
    const quiet = l => ctx.has(keyOf(l.source)) || ctx.has(keyOf(l.target));
    const grey = withAlpha(p.muted, 0.1);
    // The frontier view answers coverage, so hue carries how much is known rather than what kind
    // of thing each node is. The legend swaps with it.
    const color = it.view === 'frontier' ? coverageColor : nodeColor;
    it.graph
        .nodeVisibility(n => (!visible || visible.has(n.key)) &&
            !drawnAsRegion(n, it.groupBy, it.collapsed, it.regionKeys))
        // Containment is what the boundary says, so the edges that said it are redundant the
        // moment one is drawn. They also all radiate from a node that is no longer there.
        .linkVisibility(l => l.shown !== false && !containmentIntoRegion(l, it.groupBy, it.collapsed, it.regionKeys))
        // A context node is here to give a relationship its other end, not because it answered
        // the reader's question, and must not read as one that did.
        .nodeColor(n => (em && !em.has(n.key)) ? p.muted
            : n.context ? withAlpha(p.muted, 0.4) : color(n, p))
        .nodeVal(n => nodeRadius(n, it.selected))
        .linkColor(l => !lit(l) ? faded(edgeColor(l, p)) : quiet(l) ? grey : edgeColor(l, p))
        .linkOpacity(0.65)
        .linkWidth(0)
        // Direction is the whole point of a dependency edge, so the arrow is never conditional --
        // except on an edge that is only context, where thousands of arrowheads would bury the
        // few the reader asked for, and cost geometry per link to do it.
        .linkDirectionalArrowLength(l => lit(l) && !quiet(l) ? 2.6 : 0)
        .linkDirectionalArrowRelPos(0.92)
        .linkDirectionalArrowResolution(4)
        .linkDirectionalArrowColor(l => quiet(l) ? grey : edgeColor(l, p))
        // Animated particles are expensive per link, so they are spent only on the selection,
        // where the cost is bounded by that one node's degree.
        .linkDirectionalParticles(l => particleCount(it, l))
        .linkDirectionalParticleWidth(1.4)
        .linkDirectionalParticleSpeed(particleSpeed)
        .linkDirectionalParticleColor(l => edgeColor(l, p));

    if (it.shapes) {
        // Returning null falls back to the library's own sphere, which is what happens until a
        // material class has been harvested from the first default render.
        it.graph.nodeThreeObject(n => buildNodeObject(it, n));
    }
}








export function setMode(elementId, use3d, spec) {
    const it = instances.get(elementId);
    if (!it || it.use3d === use3d) return;
    const palette = it.palette, view = it.view;
    create(elementId, use3d, it.dotNetRef);
    const next = instances.get(elementId);
    if (!next) return;
    next.palette = palette;
    next.view = view;
    // A z coordinate only means anything in 3D, so the layout is re-seeded for the new mode.
    if (next.all) {
        for (const n of next.all.nodes) n.x = undefined;
        next.seeded = false;
        applyFilters(elementId, spec);
    }
}

export function focusOn(elementId, key) {
    const it = instances.get(elementId);
    if (!it) return;
    const node = it.graph.graphData().nodes.find(n => n.key === key);
    if (node) focusNode(it, node);
}

/**
 * Moves the camera to a node.
 *
 * In 2D the scene is the z=0 plane, so the camera has to stay on the +z axis and only pan. The
 * 3D path aims along the node's own vector, which in 2D would put the camera *in* the plane and
 * collapse the whole graph to a line.
 */
function focusNode(it, node) {
    const x = node.x || 0, y = node.y || 0, z = node.z || 0;

    if (!it.use3d) {
        const current = it.graph.cameraPosition();
        const distance = Math.max(120, Math.abs(current.z) * 0.45);
        it.graph.cameraPosition({ x, y, z: distance }, { x, y, z: 0 }, 700);
        return;
    }

    const ratio = 1 + 260 / (Math.hypot(x, y, z) || 1);
    it.graph.cameraPosition({ x: x * ratio, y: y * ratio, z: z * ratio }, node, 700);
}

/** Marks nodes as selected without changing what the view contains. */
export function setSelection(elementId, keys) {
    const it = instances.get(elementId);
    if (!it) return;
    it.selected = new Set((Array.isArray(keys) ? keys : [keys]).filter(Boolean));
    applySelection(it);
    wake(it);
}

/**
 * Selecting dims rather than removes.
 *
 * The two gestures differ in what they cost the reader: a single click leaves everything where
 * it is and merely turns the context down, so the selection can be judged against its
 * surroundings and undone by looking elsewhere. Isolating rebuilds the view, so it stays behind
 * a deliberate second gesture.
 */
function selectNode(it, key, opts) {
    const o = opts || {};
    if (!it.selected) it.selected = new Set();
    if (o.toggle) {
        if (!it.selected.delete(key)) it.selected.add(key);
    } else if (o.add) {
        it.selected.add(key);
    } else {
        it.selected = new Set([key]);
    }
    applySelection(it, !o.toggle && !o.add);
}

function applySelection(it, frame) {
    // Emphasis is the union, so comparing several nodes shows everything they collectively
    // touch rather than only the last one clicked.
    it.emphasis = null;
    if (it.selected && it.selected.size && it.all) {
        const union = new Map();
        for (const key of it.selected) {
            for (const [k, hop] of neighborhood(it, key, 1, 'both')) {
                if (!union.has(k) || hop < union.get(k)) union.set(k, hop);
            }
        }
        it.emphasis = union;
    }
    paint(it);
    startParticleCycle(it);
    // Meshes carry their own color and outline, so a change of selection has to rebuild them.
    if (it.shapes) it.graph.refresh();
    if (frame !== false && it.emphasis) frameKeys(it, it.emphasis);
}

/** Frames a set of nodes, keeping the camera's current direction so the view does not spin. */
function frameKeys(it, keys) {
    const nodes = it.graph.graphData().nodes
        .filter(n => keys.has(n.key) && n.x !== undefined);
    if (!nodes.length) return;

    let cx = 0, cy = 0, cz = 0;
    for (const n of nodes) { cx += n.x; cy += n.y; cz += n.z || 0; }
    cx /= nodes.length; cy /= nodes.length; cz /= nodes.length;

    let r = 0;
    for (const n of nodes) r = Math.max(r, Math.hypot(n.x - cx, n.y - cy, (n.z || 0) - cz));
    const distance = Math.max(140, r * 2.4);

    if (!it.use3d) {
        it.graph.cameraPosition({ x: cx, y: cy, z: distance }, { x: cx, y: cy, z: 0 }, 700);
        return;
    }
    const cam = it.graph.cameraPosition();
    const dx = cam.x - cx, dy = cam.y - cy, dz = cam.z - cz;
    const len = Math.hypot(dx, dy, dz) || 1;
    it.graph.cameraPosition(
        { x: cx + dx / len * distance, y: cy + dy / len * distance, z: cz + dz / len * distance },
        { x: cx, y: cy, z: cz }, 700);
}

/**
 * Where the camera goes when a zoom changes its distance but not its direction.
 *
 * Direction is kept for the same reason `frameKeys` keeps it: a zoom that also rotates loses the
 * reader's place in a view where most nodes look alike. The floor stops a repeated zoom-in from
 * walking the camera through the scene and coming out inverted behind it.
 */
export function zoomTarget(cam, target, factor, min = 80) {
    const dx = cam.x - target.x, dy = cam.y - target.y, dz = cam.z - target.z;
    const len = Math.hypot(dx, dy, dz) || 1;
    const k = Math.max(min, len * factor) / len;
    return { x: target.x + dx * k, y: target.y + dy * k, z: target.z + dz * k };
}

/** Moves the camera in or out around whatever the controls are currently orbiting. */
function zoomBy(it, factor) {
    const controls = it.graph.controls();
    const live = controls && controls.target;
    // A copy, never the controls' own vector: the renderer writes the look-at it is given into
    // that same object as it tweens, so passing the live target makes it chase the camera.
    const t = live ? { x: live.x, y: live.y, z: live.z } : { x: 0, y: 0, z: 0 };
    it.graph.cameraPosition(zoomTarget(it.graph.cameraPosition(), t, factor), t, 350);
}

export function fit(elementId) {
    const it = instances.get(elementId);
    if (it) fitCore(it);
}

export function dispose(elementId, keepEntry) {
    const it = instances.get(elementId);
    if (!it) return;
    it.running = false;
    clearInterval(it.particleTimer);
    if (it.onResize) window.removeEventListener('resize', it.onResize);
    if (it.onScroll) window.removeEventListener('scroll', it.onScroll);
    if (it.onWake && it.el) it.el.removeEventListener('wheel', it.onWake);
    // A rebuild binds a new one, and the Explorer rebuilds on every filter change: left behind,
    // each surviving handler zooms again on the same gesture and the camera leaves the scene.
    if (it.onDoubleClick && it.el) it.el.removeEventListener('dblclick', it.onDoubleClick);
    if (it.el) {
        if (it.onPointerMove) it.el.removeEventListener('pointermove', it.onPointerMove);
        if (it.onPointerLeave) it.el.removeEventListener('pointerleave', it.onPointerLeave);
        if (it.onPointerDown) it.el.removeEventListener('pointerdown', it.onPointerDown);
        if (it.onPointerUp) it.el.removeEventListener('pointerup', it.onPointerUp);
        if (it.onClick) it.el.removeEventListener('click', it.onClick);
        if (it.onContextMenu) it.el.removeEventListener('contextmenu', it.onContextMenu);
    }
    if (it.onKeyDown) document.removeEventListener('keydown', it.onKeyDown);
    try { it.observer && it.observer.disconnect(); } catch { /* already gone */ }
    try { it.graph._destructor && it.graph._destructor(); } catch { /* already gone */ }
    const el = document.getElementById(elementId);
    if (el) el.innerHTML = '';
    if (!keepEntry) instances.delete(elementId);
}
