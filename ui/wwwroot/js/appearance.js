// What a node looks like: color, size and the mesh that carries both.

export const SHA = /^[0-9a-f]{40}$/i;

/**
 * Color answers one question: what kind of thing is this.
 *
 * Origin and externality overlap — an upstream reusable workflow is both — so neither can carry
 * a legend on its own. Kind is disjoint by construction, and it is what a reader is trying to
 * tell apart first. Selection is shown by a ring instead, because it is a state rather than a
 * category and must not compete with the categories for hue.
 */
export function nodeColor(node, palette) {
    return (palette.kinds && palette.kinds[node.kind]) || palette.muted;
}

/**
 * Color by place in the discovery queue, for the view that is about coverage.
 *
 * Most nodes were never queued: they are workflows and steps inside the corpus under review,
 * not dependencies of it. They get the muted color, because "not a dependency" is a fact and
 * must not be drawn in the same family as "we have not looked yet".
 */
export function discoveryColor(node, palette) {
    const states = palette.discovery || {};
    return states[node.discovery] || palette.muted;
}

/**
 * Color by how much of a node has been analyzed, for the view whose legend promises exactly that.
 *
 * Queue state answers "has anything been looked at"; collapsed groups need the summed coverage
 * ratio because they carry no single queue state.
 *
 * No references in use keeps the state, because "not a dependency" is a fact about a workflow
 * inside the corpus under review and must not be drawn as "we have not looked yet".
 */
export function coverageColor(node, palette) {
    const inUse = node.refsInUse || 0;
    if (inUse <= 0) return discoveryColor(node, palette);
    const states = palette.discovery || {};
    const done = node.refsAnalyzed || 0;
    if (done >= inUse) return states.done || palette.muted;
    if (done <= 0) return states.queued || palette.muted;
    // Partly covered is its own answer, not a rounding of either end.
    return mix(states.queued || palette.muted, states.done || palette.muted, done / inUse);
}

/** Blends two colors, so a partial coverage reads as between its two ends rather than beside them. */
function mix(from, to, t) {
    const a = channels(from), b = channels(to);
    if (!a || !b) return from;
    const at = (i) => Math.round(a[i] + (b[i] - a[i]) * t);
    return `rgb(${at(0)},${at(1)},${at(2)})`;
}

function channels(color) {
    const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec((color || '').trim());
    if (hex) {
        const h = hex[1].length === 3 ? hex[1].replace(/./g, c => c + c) : hex[1];
        const n = parseInt(h, 16);
        return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
    }
    const rgb = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i.exec((color || '').trim());
    return rgb ? [+rgb[1], +rgb[2], +rgb[3]] : null;
}

export function edgeColor(link, palette) {
    switch (link.cls) {
        case 'sameRepo': return palette.edgeSameRepo;
        case 'sameOwner': return palette.edgeSameOwner;
        default: return palette.edgeCrossOwner;
    }
}

/** Drops an rgba color's alpha, so a dimmed edge recedes without changing hue. */
export function faded(color) {
    if (typeof color !== 'string') return color;
    if (color.startsWith('rgba')) return color.replace(/[\d.]+\)\s*$/, '0.06)');
    if (color.startsWith('rgb(')) return color.replace('rgb(', 'rgba(').replace(')', ',0.06)');
    return color;
}

/**
 * The same color at a chosen weight.
 *
 * `faded` has one weight, 0.06, which is right for emphasis and far too faint for an edge that is
 * still meant to be followed. The palette hands out hex and both rgb notations, so reading all
 * three is the difference between de-emphasizing an edge and silently leaving it at full strength.
 */
export function withAlpha(color, a) {
    if (typeof color !== 'string') return color;
    const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color.trim());
    if (hex) {
        const h = hex[1].length === 3 ? hex[1].replace(/./g, c => c + c) : hex[1];
        const n = parseInt(h, 16);
        return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
    }
    const rgb = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i.exec(color.trim());
    return rgb ? `rgba(${rgb[1]},${rgb[2]},${rgb[3]},${a})` : color;
}

/** Reach spans orders of magnitude, so radius follows a log scale or hubs swamp everything. */
export function nodeRadius(node, selected) {
    // An owner is a grouping device, not something a workflow can depend on. Sizing it by its
    // largest member drew the container half again as large as the dependency inside it, and
    // counted that member's reach twice on screen.
    let base;
    if (node.kind === 'org') {
        base = 3.4;
    } else if (node.reachKnown === false) {
        // Arrived since reach was last measured. Drawing it at the smallest size would say
        // nothing depends on it, which is a measurement nobody has made yet, so it sits at the
        // middle of the scale until one has been.
        base = 3.2;
    } else {
        base = 2 + Math.log2(1 + (node.dependentRepos || 0)) * 1.4;
    }
    // Barely enlarged. Size carries reach, so growing a node on selection made it two sizes for
    // a reason that has nothing to do with reach, and up close it filled the canvas.
    return isSelected(selected, node.key) ? base * 1.25 : base;
}

/** Selection is a set, but most callers hold one key or none. */
export function isSelected(selected, key) {
    if (!selected) return false;
    return typeof selected === 'string' ? selected === key : selected.has(key);
}

/**
 * Geometry per kind, so shape carries the same meaning as color.
 *
 * Redundant encoding is the point: color alone fails for a color-blind reader, and at this
 * density hue is hard to judge against neighboring hues anyway.
 *
 * Built from the separately vendored THREE. The graph library bundles its own copy, but plain
 * Mesh + BufferGeometry + Material objects interoperate across copies; `shapesUsable` proves it
 * at runtime rather than assuming it.
 */
const KIND_GEOMETRY = {
    // Not a torus: a ring is what marks the selection, and the two were indistinguishable. A
    // solid drum rather than a flat disc, which turned edge-on into a slab as the camera moved.
    org: T => new T.CylinderGeometry(1.15, 1.15, 1.5, 5),
    repo: T => new T.BoxGeometry(1.7, 1.7, 1.7),
    workflow: T => new T.SphereGeometry(1, 12, 10),
    reusable_workflow: T => new T.OctahedronGeometry(1.25),
    composite_action: T => new T.ConeGeometry(1.2, 2.2, 8),
    action: T => new T.IcosahedronGeometry(1.15),
    docker_image: T => new T.CylinderGeometry(1, 1, 1.8, 10),
    step: T => new T.TetrahedronGeometry(1.2),
};

let shapeCache = null;

export function shapesUsable() {
    return typeof window !== 'undefined' && window.THREE
        && typeof window.THREE.Mesh === 'function';
}

export function geometryFor(kind) {
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
 * but a material does not: the renderer does not recognize a foreign material type and silently
 * falls back to white. Harvesting the class from a mesh the library itself built keeps color
 * working while still using the vendored geometry.
 */
export function internalMaterialClass(it) {
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

export function buildNodeObject(it, node) {
    const T = window.THREE;
    const Material = internalMaterialClass(it);
    if (!Material) return null;

    // Reassigning nodeThreeObject rebuilds every mesh, so dimming has to be decided here;
    // mutating the material afterwards is discarded by the rebuild that follows.
    const lit = !it.emphasis || it.emphasis.has(node.key);
    const picked = isSelected(it.selected, node.key);
    const geometry = geometryFor(node.kind);
    // The mesh carries its own color, so a view that recolors has to be honored here as well as
    // on the accessor; the accessor alone is ignored the moment shapes are in use.
    const color = it.view === 'frontier' ? coverageColor : nodeColor;
    const mesh = new T.Mesh(
        geometry,
        new Material({
            color: lit ? color(node, it.palette) : it.palette.muted,
            transparent: true,
            // A selected node is see-through: opaque, it hid the neighborhood it was chosen
            // to reveal the moment the camera came close.
            opacity: picked ? 0.45 : (lit ? 0.9 : 0.16),
            depthWrite: !picked,
        }));
    const s = nodeRadius(node, it.selected);
    mesh.scale.set(s, s, s);

    if (picked) {
        // An outline traced from the node's own geometry, so the marker fits whatever silhouette
        // the node has. A ring only ever suited a sphere, and collided with any node drawn as
        // one.
        const hull = new T.Mesh(geometry, new Material({
            color: it.palette.focus,
            transparent: true,
            opacity: 0.85,
            side: T.BackSide,
            depthWrite: false,
        }));
        hull.scale.set(1.22, 1.22, 1.22);
        mesh.add(hull);
    }
    return mesh;
}
