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
    seed, repoOf, orgOf, edgeClass, groupOf, anchorKey, keyOf, touches, touchesAny,
} from './topology.js';
import {
    nodeColor, edgeColor, faded, nodeRadius, isSelected, buildNodeObject, shapesUsable,
    internalMaterialClass, SHA,
} from './appearance.js';
import { applyForces } from './forces.js';
import { collapseGroups } from './collapse.js';
import { particleCount, particleSpeed, startParticleCycle } from './particles.js';

// Re-exported so the pure decisions can be imported by name from one place, including by tests.
export {
    seed, repoOf, orgOf, edgeClass, groupOf, anchorKey, keyOf,
    faded, nodeRadius, isSelected, particleSpeed,
};

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















/** The sole selected key when exactly one is selected, otherwise empty. */
function soleSelection(it) {
    return it.selected && it.selected.size === 1 ? [...it.selected][0] : '';
}

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
export async function loadFull(elementId, url) {
    const it = instances.get(elementId);
    if (!it) return null;

    if (!it.cache[url]) {
        const res = await fetch(url);
        if (!res.ok) throw new Error(`${url} responded ${res.status}`);
        const data = await res.json();

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
            // filtering by relationship is a set lookup.
            e.cls = edgeClass(s, t);
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
        it.cache[url] = { nodes: data.nodes, links, degree, rules: [...rules].sort() };
    }

    it.all = it.cache[url];
    it.degree = it.all.degree;
    it.adjacency = null;
    // A different node set has to be handed to the renderer rather than merely re-hidden.
    it.seeded = false;
    it.renderedFor = null;
    return {
        nodes: it.all.nodes.length,
        edges: it.all.links.length,
        rules: it.all.rules,
    };
}

function adjacencyOf(it) {
    if (it.adjacency) return it.adjacency;
    const adj = new Map();
    const link = (a, b) => {
        if (!adj.has(a)) adj.set(a, []);
        adj.get(a).push(b);
    };
    for (const e of it.all.links) {
        link(e.source, e.target);
        link(e.target, e.source);
    }
    it.adjacency = adj;
    return adj;
}

/** Everything reachable from a selection, in both directions, out to a hop limit. */
function neighborhood(it, key, hops) {
    const adj = adjacencyOf(it);
    const seen = new Map([[key, 0]]);
    let frontier = [key];
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
export function applyFilters(elementId, spec) {
    const it = instances.get(elementId);
    if (!it || !it.all) return { nodes: 0, edges: 0, hidden: 0, total: 0 };

    const f = spec || {};
    const total = it.all.nodes.length;
    const reachable = f.selection ? neighborhood(it, f.selection, f.hops || 0) : null;

    const search = (f.search || '').toLowerCase();
    // Explicit allow-lists: the panel always sends what is checked, so an empty list genuinely
    // means "show none" rather than "no opinion".
    const kinds = new Set(f.kinds || []);
    // Links saved before the classes were renamed still name the old keys; accepting both keeps
    // an old address from silently emptying the graph.
    const edgeClasses = new Set((f.edgeClasses || []).map(c =>
        c === 'externalOrg' ? 'crossOwner' : c === 'sameOrg' ? 'sameOwner' : c));
    const origins = new Set(f.origins || []);
    const rules = new Set(f.rules || []);

    const visible = new Set();
    for (const n of it.all.nodes) {
        if (reachable && !reachable.has(n.key)) continue;
        if (!kinds.has(n.kind)) continue;
        if (origins.size && !origins.has(n.origin || 'external')) continue;
        if (search && !n.key.toLowerCase().includes(search)) continue;
        if (f.hasFindings && !n.findings) continue;
        if (f.hasAdvisory && !n.advisories) continue;
        if (f.unanalyzedOnly && n.analyzed) continue;
        if (f.minReach && (n.dependentRepos || 0) < f.minReach) continue;
        if (rules.size && !(n.rules || []).some(r => rules.has(r))) continue;
        if (f.refStyle && f.refStyle !== 'any' && !n.refStyles.has(f.refStyle)) continue;
        visible.add(n.key);
    }

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
    // mean nothing.
    it.selected = f.selection ? new Set([f.selection]) : new Set();
    it.hopsOf = reachable;
    it.visible = visible;
    // Isolating already removed the unrelated, so dimming on top of it would only mute what is
    // left. Emphasis belongs to selection-without-narrowing.
    it.emphasis = null;

    // Hidden nodes normally stay in the simulation so that positions hold still across a
    // filter. That stops paying for itself once most of the graph is hidden: arriving on a
    // 45-node view should not cost a 3,969-node settle. Below the threshold the simulation gets
    // only what is shown, and coordinates are kept on the nodes so returning is free.
    const subset = visible.size < total * SUBSET_FRACTION;

    if (it.seeded && subset === it.subset) {
        paint(it);
        it.graph.refresh();
    } else {
        render(it, subset);
    }
    if (it.collapsed) {
        const shown = it.graph.graphData();
        return { nodes: shown.nodes.length, edges: shown.links.length, hidden: 0, total };
    }
    return { nodes: visible.size, edges, hidden: total - visible.size, total };
}

/** Below this share of the corpus, the simulation is given only what is on screen. */
const SUBSET_FRACTION = 0.25;

/** Seeds positions and hands the node set to the renderer. */
function render(it, subset) {
    let nodes = subset
        ? it.all.nodes.filter(n => it.visible.has(n.key))
        : it.all.nodes;
    let links = subset
        ? it.all.links.filter(e => e.shown)
        : it.all.links;
    let collapsed = null;
    if (it.collapsed) {
        collapsed = collapseGroups(it, nodes, it.all.links);
        nodes = collapsed.nodes;
        links = collapsed.links;
    }
    it.subset = !!subset;
    it.maxPriority = Math.max(1, ...nodes.map(n => n.priority || 0));

    // The layout's natural span grows with the node count, so the seed radius has to as well.
    // Seeding thousands of nodes into a small ball leaves the simulation pushing outward from a
    // singularity, and it settles as a speck.
    const spread = Math.max(300, Math.sqrt(nodes.length) * 34);
    let fresh = 0;
    for (const n of nodes) {
        if (n.x === undefined) {
            fresh++;
            const angle = seed(n.key) * Math.PI * 2;
            const r = spread * (0.35 + seed(n.key + '#r') * 0.65);
            n.x = Math.cos(angle) * r;
            n.y = Math.sin(angle) * r;
            n.z = (seed(n.key + '#z') - 0.5) * (it.use3d ? spread : 0);
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
    it.pendingFit = true;
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

function fitHost(el) {
    if (!el || !el.isConnected) return;
    const top = el.getBoundingClientRect().top + window.scrollY;
    el.style.height = `${Math.max(360, Math.round(window.innerHeight - top - CAPTION_GUTTER))}px`;
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
        .nodeLabel(n => `${n.key}\n${n.kind}${n.dependentRepos ? ` · ${n.dependentRepos} repos` : ''}`)
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
        // A single click only moves the camera. Rebuilding the whole view is a big enough change
        // that it should take a deliberate second action.
        .onNodeClick((n, event) => {
            const cur = instances.get(elementId);
            if (!n || !cur) return;
            const now = performance.now();
            const isDouble = cur.lastClickKey === n.key && now - cur.lastClickAt < 350;
            cur.lastClickKey = n.key;
            cur.lastClickAt = now;
            if (isDouble) {
                if (dotNetRef) dotNetRef.invokeMethodAsync('OnNodeIsolated', n.key);
                return;
            }
            // Cmd on macOS, Ctrl elsewhere, toggles; Shift adds without removing.
            const toggle = !!(event && (event.metaKey || event.ctrlKey));
            const add = !!(event && event.shiftKey);
            selectNode(cur, n.key, { toggle, add });
            if (dotNetRef) dotNetRef.invokeMethodAsync('OnNodeSelected', [...cur.selected]);
        })
        .onNodeRightClick(n => {
            if (n && dotNetRef) dotNetRef.invokeMethodAsync('OnNodeIsolated', n.key);
        })
        .onNodeHover(n => {
            const cur = instances.get(elementId);
            if (cur) cur.hovered = n ? n.key : '';
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
        });

    const controls = graph.controls();
    if (controls) {
        controls.zoomSpeed = 2.4;
        // In 2D the scene is a plane. Left free to rotate it can be tumbled edge-on, where it
        // occupies no pixels and reads as a blank canvas.
        controls.noRotate = !use3d;
    }
    graph.cameraPosition({ x: 0, y: 0, z: 1200 }, { x: 0, y: 0, z: 0 });

    const labels = document.createElement('canvas');
    labels.className = 'abom-graph-labels';
    el.appendChild(labels);

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

    const it = {
        graph, observer, onResize, el, labels, use3d, dotNetRef,
        cache: carried.cache, all: carried.all, adjacency: null,
        degree: carried.all ? carried.all.degree : new Map(),
        selected: new Set(), hovered: '', hopsOf: null, view: 'neighborhood', palette: null,
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
function drawLabels(it) {
    if (!it.running) return;
    requestAnimationFrame(() => drawLabels(it));

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

    // The selection marker is drawn in the scene as an outline on the node's own shape, so
    // nothing about it belongs here. Labels only.
    const shown = new Set(it.selected || []);
    if (it.hovered) shown.add(it.hovered);

    for (const key of shown) {
        const n = nodes.find(o => o.key === key);
        if (!n || n.x === undefined) continue;
        const pos = it.graph.graph2ScreenCoords(n.x, n.y, n.z || 0);
        if (!pos) continue;

        const picked = isSelected(it.selected, n.key);
        const label = n.key.length > 60 ? n.key.slice(0, 59) + '…' : n.key;
        const size = 13;
        ctx.font = `${picked ? '600 ' : ''}${size}px system-ui, sans-serif`;

        const w = ctx.measureText(label).width;
        const y = pos.y + nodeRadius(n, it.selected) + size + 4;

        ctx.fillStyle = p.background;
        ctx.globalAlpha = 0.92;
        ctx.beginPath();
        if (ctx.roundRect) ctx.roundRect(pos.x - w / 2 - 5, y - size / 2 - 4, w + 10, size + 8, 4);
        else ctx.rect(pos.x - w / 2 - 5, y - size / 2 - 4, w + 10, size + 8);
        ctx.fill();
        ctx.globalAlpha = 1;

        ctx.fillStyle = picked ? p.focus : p.label;
        ctx.fillText(label, pos.x, y);
    }
}

/**
 * Frames the dense part of the graph rather than its full extent.
 *
 * A handful of stragglers sit far outside everything else, and fitting to them shrinks the part
 * worth looking at to a few pixels while leaving zoom with nowhere useful to go.
 */
function fitCore(it, percentile) {
    const nodes = it.graph.graphData().nodes.filter(n => n.x !== undefined);
    if (!nodes.length) return;

    const radii = nodes.map(n => Math.hypot(n.x, n.y, n.z || 0)).sort((a, b) => a - b);
    const r = Math.max(60, radii[Math.floor(radii.length * (percentile || 0.9))] || 100);
    it.graph.cameraPosition({ x: 0, y: 0, z: r * 2.1 }, { x: 0, y: 0, z: 0 }, 600);
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
    it.view = view;
    applyForces(it);
    paint(it);
    it.graph.d3ReheatSimulation();
}

export function setPriorities(elementId, priorities) {
    const it = instances.get(elementId);
    if (!it || !it.all) return;
    for (const n of it.all.nodes) {
        n.priority = priorities[n.key] || 0;
    }
}






/** Chooses what nodes are pulled together by, or `none` to leave the layout alone. */
export function setGroupBy(elementId, by) {
    const it = instances.get(elementId);
    if (!it || it.groupBy === by) return;
    it.groupBy = by || 'none';
    applyForces(it);
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



function paint(it) {
    const p = it.palette;
    if (!p) return;

    const visible = it.visible;
    const em = it.emphasis;
    const lit = l => !em || em.has(keyOf(l.source)) || em.has(keyOf(l.target));
    it.graph
        .nodeVisibility(n => !visible || visible.has(n.key))
        .linkVisibility(l => l.shown !== false)
        .nodeColor(n => (em && !em.has(n.key)) ? p.muted : nodeColor(n, p))
        .nodeVal(n => nodeRadius(n, it.selected))
        .linkColor(l => lit(l) ? edgeColor(l, p) : faded(edgeColor(l, p)))
        .linkOpacity(0.65)
        .linkWidth(0)
        // Direction is the whole point of a dependency edge, so the arrow is never conditional.
        // It sits just short of the target so the head is not swallowed by the node.
        .linkDirectionalArrowLength(l => lit(l) ? 2.6 : 0)
        .linkDirectionalArrowRelPos(0.92)
        .linkDirectionalArrowResolution(4)
        .linkDirectionalArrowColor(l => edgeColor(l, p))
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
            for (const [k, hop] of neighborhood(it, key, 1)) {
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

export function fit(elementId) {
    const it = instances.get(elementId);
    if (it) fitCore(it);
}

/** Viewport position of a node, so a caller can drive a real click at real coordinates. */
export function nodeScreenPosition(elementId, key) {
    const it = instances.get(elementId);
    if (!it) return null;
    const node = it.graph.graphData().nodes.find(n => n.key === key);
    if (!node || node.x === undefined) return null;
    const p = it.graph.graph2ScreenCoords(node.x, node.y, node.z || 0);
    if (!p) return null;
    const box = it.el.getBoundingClientRect();
    return { x: box.left + p.x, y: box.top + p.y };
}

export function nodeKeys(elementId) {
    const it = instances.get(elementId);
    return it ? it.graph.graphData().nodes.map(n => n.key) : [];
}

/** Frames rendered over a sampling window, for benchmarking at scale. */
export function sampleFrameRate(ms) {
    return new Promise(resolve => {
        let frames = 0;
        const start = performance.now();
        const tick = () => {
            frames++;
            const elapsed = performance.now() - start;
            if (elapsed < ms) requestAnimationFrame(tick);
            else resolve(Math.round(frames * 1000 / elapsed));
        };
        requestAnimationFrame(tick);
    });
}

export function dispose(elementId, keepEntry) {
    const it = instances.get(elementId);
    if (!it) return;
    it.running = false;
    clearInterval(it.particleTimer);
    if (it.onResize) window.removeEventListener('resize', it.onResize);
    try { it.observer && it.observer.disconnect(); } catch { /* already gone */ }
    try { it.graph._destructor && it.graph._destructor(); } catch { /* already gone */ }
    const el = document.getElementById(elementId);
    if (el) el.innerHTML = '';
    if (!keepEntry) instances.delete(elementId);
}
