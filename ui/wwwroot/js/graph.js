// WebGL rendering for the ABOM dependency graph.
//
// One renderer, two modes: 3D, and 2D as the same WebGL scene constrained to a plane. A single
// code path is what makes it worth investing in labels, picking and projection.
//
// One node set, one *semantic* force at a time. Structural forces (link, charge) are always on
// and come from the edges. A semantic force is a positional constraint derived from a metric,
// and only ever one is active: if position encoded two things at once you could not tell whether
// a node sits at the centre because it is urgent or because it is popular.
//
// Switching view changes only the target radius, never the angle, so nodes travel along a ray
// and stay trackable. That is what makes a switch read as re-orientation rather than reshuffle.

const instances = new Map();

const SHA = /^[0-9a-f]{40}$/i;

/** Deterministic per-key seed, so the same corpus always lays out identically. */
function seed(key) {
    let h = 2166136261;
    for (let i = 0; i < key.length; i++) {
        h ^= key.charCodeAt(i);
        h = Math.imul(h, 16777619);
    }
    return (h >>> 0) / 4294967295;
}

/** Pulls each node towards a radius the active view decides. */
function radialForce(radiusOf, strength) {
    let nodes = [];
    function force(alpha) {
        for (const n of nodes) {
            const target = radiusOf(n);
            if (target === null || target === undefined) continue;
            const x = n.x || 1e-6, y = n.y || 1e-6, z = n.z || 0;
            const dist = Math.sqrt(x * x + y * y + z * z) || 1e-6;
            const k = (target - dist) * strength * alpha / dist;
            n.vx += x * k;
            n.vy += y * k;
            if (n.vz !== undefined) n.vz += z * k;
        }
    }
    force.initialize = (n) => { nodes = n; };
    return force;
}

/** Pulls nodes towards one of a few anchor positions, clustering by category. */
function anchorForce(anchorOf, strength) {
    let nodes = [];
    function force(alpha) {
        for (const n of nodes) {
            const a = anchorOf(n);
            if (!a) continue;
            n.vx += (a.x - (n.x || 0)) * strength * alpha;
            n.vy += (a.y - (n.y || 0)) * strength * alpha;
        }
    }
    force.initialize = (n) => { nodes = n; };
    return force;
}

/**
 * Draws nodes back towards the origin, hardest on the ones with fewest links.
 *
 * Repulsion acts on every pair but only edges pull back, so barely-connected and orphaned nodes
 * accelerate outward with nothing to stop them. They end up owning most of the bounding box,
 * which leaves the dense part of the graph rendered as a speck and makes zoom feel dead.
 */
function gravityForce(strengthOf) {
    let nodes = [];
    function force(alpha) {
        for (const n of nodes) {
            const s = strengthOf(n) * alpha;
            n.vx -= (n.x || 0) * s;
            n.vy -= (n.y || 0) * s;
            if (n.vz !== undefined) n.vz -= (n.z || 0) * s;
        }
    }
    force.initialize = (n) => { nodes = n; };
    return force;
}

/**
 * Colour answers one question: what kind of thing is this.
 *
 * Origin and externality overlap — an upstream reusable workflow is both — so neither can carry
 * a legend on its own. Kind is disjoint by construction, and it is what a reader is trying to
 * tell apart first. Selection is shown by a ring instead, because it is a state rather than a
 * category and must not compete with the categories for hue.
 */
function nodeColour(node, palette) {
    return (palette.kinds && palette.kinds[node.kind]) || palette.muted;
}

/** The repository a node belongs to. An external action carries it in its own key. */
function repoOf(node) {
    if (node.repository) return node.repository;
    const parts = (node.key || '').split('/');
    return parts.length >= 2 ? `${parts[0]}/${parts[1]}` : (node.key || '');
}

function orgOf(node) {
    return repoOf(node).split('/')[0];
}

/**
 * How far an edge reaches: within one repository, across an organisation, or out of it.
 *
 * Same repository wins over same organisation, so the closest true relationship is the one
 * reported rather than the broadest.
 */
function edgeClass(s, t) {
    if (!s || !t) return 'externalOrg';
    if (repoOf(s) === repoOf(t)) return 'sameRepo';
    return orgOf(s) === orgOf(t) ? 'sameOrg' : 'externalOrg';
}

function edgeColour(link, palette) {
    switch (link.cls) {
        case 'sameRepo': return palette.edgeSameRepo;
        case 'sameOrg': return palette.edgeSameOrg;
        default: return palette.edgeExternalOrg;
    }
}

/** Reach spans orders of magnitude, so radius follows a log scale or hubs swamp everything. */
function nodeRadius(node, selection) {
    const base = 2 + Math.log2(1 + (node.dependentRepos || 0)) * 1.4;
    return node.key === selection ? base * 2.2 : base;
}

// ---------------------------------------------------------------------------
// Shapes
// ---------------------------------------------------------------------------

/**
 * Geometry per kind, so shape carries the same meaning as colour.
 *
 * Redundant encoding is the point: colour alone fails for a colour-blind reader, and at this
 * density hue is hard to judge against neighbouring hues anyway.
 *
 * Built from the separately vendored THREE. The graph library bundles its own copy, but plain
 * Mesh + BufferGeometry + Material objects interoperate across copies; `shapesUsable` proves it
 * at runtime rather than assuming it.
 */
const KIND_GEOMETRY = {
    repo: T => new T.BoxGeometry(1.7, 1.7, 1.7),
    workflow: T => new T.SphereGeometry(1, 12, 10),
    reusable_workflow: T => new T.OctahedronGeometry(1.25),
    composite_action: T => new T.ConeGeometry(1.2, 2.2, 8),
    action: T => new T.IcosahedronGeometry(1.15),
    docker_image: T => new T.CylinderGeometry(1, 1, 1.8, 10),
    step: T => new T.TetrahedronGeometry(1.2),
};

let shapeCache = null;

function shapesUsable() {
    return typeof window !== 'undefined' && window.THREE
        && typeof window.THREE.Mesh === 'function';
}

function geometryFor(kind) {
    const T = window.THREE;
    if (!shapeCache) shapeCache = {};
    if (!shapeCache[kind]) {
        const make = KIND_GEOMETRY[kind] || KIND_GEOMETRY.workflow;
        // Cached per kind, never per node: 3.6k geometries would defeat the point entirely.
        shapeCache[kind] = make(T);
    }
    return shapeCache[kind];
}

/**
 * The material class belonging to the *renderer's* own THREE.
 *
 * Geometry crosses between the two THREE copies happily because it is plain typed-array data,
 * but a material does not: the renderer does not recognise a foreign material type and silently
 * falls back to white. Harvesting the class from a mesh the library itself built keeps colour
 * working while still using the vendored geometry.
 */
function internalMaterialClass(it) {
    if (it.materialClass !== undefined) return it.materialClass;
    let found = null;
    try {
        it.graph.scene().traverse(o => {
            if (!found && o.isMesh && o.material && o.material.constructor) {
                found = o.material.constructor;
            }
        });
    } catch { /* scene not ready yet */ }
    if (found) it.materialClass = found;
    return found;
}

function buildNodeObject(it, node) {
    const T = window.THREE;
    const Material = internalMaterialClass(it);
    if (!Material) return null;

    const mesh = new T.Mesh(
        geometryFor(node.kind),
        new Material({
            color: nodeColour(node, it.palette),
            transparent: true,
            opacity: 0.9,
        }));
    const s = nodeRadius(node, it.selection);
    mesh.scale.set(s, s, s);
    return mesh;
}

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
            // summarised onto both ends. Lexical only: whether a reference is *safe* is a
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
function neighbourhood(it, key, hops) {
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
 * A selection's neighbourhood is walked over the *whole* graph and the attribute filters are
 * then applied to the result. Walking only over already-filtered nodes would cut the path to
 * anything interesting and silently under-report reach.
 */
export function applyFilters(elementId, spec) {
    const it = instances.get(elementId);
    if (!it || !it.all) return { nodes: 0, edges: 0, hidden: 0, total: 0 };

    const f = spec || {};
    const total = it.all.nodes.length;
    const reachable = f.selection ? neighbourhood(it, f.selection, f.hops || 0) : null;

    const search = (f.search || '').toLowerCase();
    // Explicit allow-lists: the panel always sends what is checked, so an empty list genuinely
    // means "show none" rather than "no opinion".
    const kinds = new Set(f.kinds || []);
    const edgeClasses = new Set(f.edgeClasses || []);
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
        if (f.unanalysedOnly && n.analysed) continue;
        if (f.minReach && (n.dependentRepos || 0) < f.minReach) continue;
        if (rules.size && !(n.rules || []).some(r => rules.has(r))) continue;
        if (f.refStyle && f.refStyle !== 'any' && !n.refStyles.has(f.refStyle)) continue;
        visible.add(n.key);
    }

    let edges = 0;
    for (const e of it.all.links) {
        e.shown = edgeClasses.has(e.cls) && visible.has(e.source) && visible.has(e.target);
        if (e.shown) edges++;
    }

    it.selection = f.selection || '';
    it.hopsOf = reachable;
    it.visible = visible;

    // Hiding rather than rebuilding. Handing force-graph a new node set recreates every mesh
    // and restarts the simulation, which is both slow and the reason the layout re-settles
    // visibly on every filter change. A hidden link keeps its force, so positions do not move.
    if (it.seeded) {
        paint(it);
        it.graph.refresh();
    } else {
        render(it);
    }
    return { nodes: visible.size, edges, hidden: total - visible.size, total };
}

/** Seeds positions and hands the full node set to the renderer, once. */
function render(it) {
    const nodes = it.all.nodes;
    const links = it.all.links;
    it.maxPriority = Math.max(1, ...nodes.map(n => n.priority || 0));

    // The layout's natural span grows with the node count, so the seed radius has to as well.
    // Seeding thousands of nodes into a small ball leaves the simulation pushing outward from a
    // singularity, and it settles as a speck.
    const spread = Math.max(300, Math.sqrt(nodes.length) * 34);
    for (const n of nodes) {
        if (n.x === undefined) {
            const angle = seed(n.key) * Math.PI * 2;
            const r = spread * (0.35 + seed(n.key + '#r') * 0.65);
            n.x = Math.cos(angle) * r;
            n.y = Math.sin(angle) * r;
            n.z = (seed(n.key + '#z') - 0.5) * (it.use3d ? spread : 0);
        }
    }
    it.spread = spread;
    applySettleBudget(it);

    // force-graph rewrites link endpoints into object references, so it gets fresh copies and
    // the loaded graph keeps its string keys for filtering.
    it.graph.graphData({ nodes, links: links.map(e => ({ ...e })) });
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
    if (it.animate) {
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
        .onNodeClick(n => {
            const cur = instances.get(elementId);
            if (!n || !cur) return;
            const now = performance.now();
            const isDouble = cur.lastClickKey === n.key && now - cur.lastClickAt < 350;
            cur.lastClickKey = n.key;
            cur.lastClickAt = now;
            if (isDouble) {
                if (dotNetRef) dotNetRef.invokeMethodAsync('OnNodeIsolated', n.key);
            } else {
                cur.selection = n.key;
                paint(cur);
                focusNode(cur, n);
                if (dotNetRef) dotNetRef.invokeMethodAsync('OnNodeSelected', n.key);
            }
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
        selection: '', hovered: '', hopsOf: null, view: 'neighbourhood', palette: null,
        maxPriority: 1, pendingFit: false, running: true, seeded: false, visible: null,
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

    // The selection is marked by a ring rather than a colour, so that hue stays free to mean
    // kind and nothing else.
    const selected = nodes.find(o => o.key === it.selection);
    if (selected && selected.x !== undefined) {
        const pos = it.graph.graph2ScreenCoords(selected.x, selected.y, selected.z || 0);
        if (pos) {
            const r = Math.max(14, nodeRadius(selected, it.selection) * 2.4);
            ctx.strokeStyle = p.focus;
            ctx.lineWidth = 2.5;
            ctx.globalAlpha = 0.95;
            ctx.beginPath();
            ctx.arc(pos.x, pos.y, r, 0, Math.PI * 2);
            ctx.stroke();
            ctx.globalAlpha = 0.25;
            ctx.beginPath();
            ctx.arc(pos.x, pos.y, r + 5, 0, Math.PI * 2);
            ctx.stroke();
            ctx.globalAlpha = 1;
        }
    }

    // Only what the reader is pointing at or has chosen. A standing set of labels covers the
    // shape of the graph and cannot be clicked through to the node underneath.
    for (const key of new Set([it.selection, it.hovered].filter(Boolean))) {
        const n = nodes.find(o => o.key === key);
        if (!n || n.x === undefined) continue;
        const pos = it.graph.graph2ScreenCoords(n.x, n.y, n.z || 0);
        if (!pos) continue;

        const isSelected = n.key === it.selection;
        const label = n.key.length > 60 ? n.key.slice(0, 59) + '…' : n.key;
        const size = 13;
        ctx.font = `${isSelected ? '600 ' : ''}${size}px system-ui, sans-serif`;

        const w = ctx.measureText(label).width;
        const y = pos.y + nodeRadius(n, it.selection) + size + 4;

        ctx.fillStyle = p.background;
        ctx.globalAlpha = 0.92;
        ctx.beginPath();
        if (ctx.roundRect) ctx.roundRect(pos.x - w / 2 - 5, y - size / 2 - 4, w + 10, size + 8, 4);
        else ctx.rect(pos.x - w / 2 - 5, y - size / 2 - 4, w + 10, size + 8);
        ctx.fill();
        ctx.globalAlpha = 1;

        ctx.fillStyle = isSelected ? p.focus : p.label;
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

/** Whether the layout is watched as it settles, or computed before anything is drawn. */
export function setAnimate(elementId, animate) {
    const it = instances.get(elementId);
    if (!it) return;
    it.animate = animate !== false;
    applySettleBudget(it);
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
 * Rebuilds node meshes after a colour change.
 *
 * A custom shape holds its colour in the material it was created with, so repainting the
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

function applyForces(it) {
    const g = it.graph;
    const spread = it.spread || 600;
    const degree = it.degree || new Map();
    // No distance cap: capping repulsion below the graph's own span means the outer nodes feel
    // nothing and the whole layout collapses inward into a speck.
    g.d3Force('charge').strength(-90).distanceMax(Infinity);
    g.d3Force('link').distance(36).strength(0.6);
    g.d3Force('gravity', gravityForce(n => 0.012 + 0.075 / (1 + (degree.get(n.key) || 0))));
    g.d3Force('semantic', null);

    if (it.view === 'priority') {
        const max = it.maxPriority;
        g.d3Force('semantic', radialForce(
            n => n.key === it.selection ? 0 : spread * 0.15
                + (1 - (n.priority || 0) / max) * spread, 0.09));
    } else if (it.view === 'ownership') {
        // Distance, not left-versus-right: an axis stops meaning anything the moment the camera
        // orbits, and this has to read the same in 2D and 3D. Reached directly sits close in,
        // reached through an intermediary sits out.
        g.d3Force('semantic', radialForce(n => {
            const hops = it.hopsOf ? it.hopsOf.get(n.key) : undefined;
            if (hops === undefined) return null;
            if (hops === 0) return 0;
            return hops === 1 ? spread * 0.3 : spread;
        }, 0.09));
    } else if (it.view === 'distance') {
        g.d3Force('semantic', radialForce(n => {
            const hops = it.hopsOf ? it.hopsOf.get(n.key) : undefined;
            return hops === undefined ? null : spread * 0.2 + hops * spread * 0.42;
        }, 0.11));
    }
}

function paint(it) {
    const p = it.palette;
    if (!p) return;

    const visible = it.visible;
    it.graph
        .nodeVisibility(n => !visible || visible.has(n.key))
        .linkVisibility(l => l.shown !== false)
        .nodeColor(n => nodeColour(n, p))
        .nodeVal(n => nodeRadius(n, it.selection))
        .linkColor(l => edgeColour(l, p))
        .linkOpacity(0.65)
        .linkWidth(0)
        // Direction is the whole point of a dependency edge, so the arrow is never conditional.
        // It sits just short of the target so the head is not swallowed by the node.
        .linkDirectionalArrowLength(2.6)
        .linkDirectionalArrowRelPos(0.92)
        .linkDirectionalArrowResolution(4)
        .linkDirectionalArrowColor(l => edgeColour(l, p))
        // Animated particles are expensive per link, so they are spent only on the selection,
        // where the cost is bounded by that one node's degree.
        .linkDirectionalParticles(l => it.selection && touches(l, it.selection) ? 2 : 0)
        .linkDirectionalParticleWidth(1.6)
        .linkDirectionalParticleSpeed(0.012)
        .linkDirectionalParticleColor(l => edgeColour(l, p));

    if (it.shapes) {
        // Returning null falls back to the library's own sphere, which is what happens until a
        // material class has been harvested from the first default render.
        it.graph.nodeThreeObject(n => buildNodeObject(it, n));
    }
}

function touches(link, key) {
    const s = link.source, t = link.target;
    return (s && (s.key || s) === key) || (t && (t.key || t) === key);
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

/** Marks a node as selected without changing what the view contains. */
export function setSelection(elementId, key) {
    const it = instances.get(elementId);
    if (!it) return;
    it.selection = key || '';
    paint(it);
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
    if (it.onResize) window.removeEventListener('resize', it.onResize);
    try { it.observer && it.observer.disconnect(); } catch { /* already gone */ }
    try { it.graph._destructor && it.graph._destructor(); } catch { /* already gone */ }
    const el = document.getElementById(elementId);
    if (el) el.innerHTML = '';
    if (!keepEntry) instances.delete(elementId);
}
