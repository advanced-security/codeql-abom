// A container whose children are visible is a region enclosing them, not a point beside them.
//
// Pure geometry and membership over screen-space positions, so both are testable without a
// renderer, and so the same code serves 2D and 3D: the caller has already projected.

import { groupOf, ungroupedName } from './topology.js';

/**
 * Members per region, keyed by group.
 *
 * Membership is `collapse.js`'s to decide, never this module's. A region and the node a collapsed
 * view folds the same members into are two renderings of one fold, and a second rule here is how
 * they would come to disagree about who belongs together.
 *
 * The ungrouped bucket is not a region: nothing contains it. A dependency carries no grouping at
 * all, because a placement is read from the organization rather than guessed from an edge.
 */
export function regionMembers(nodes, by) {
    const out = new Map();
    if (!by || by === 'none') return out;
    const ungrouped = ungroupedName(by);

    for (const n of nodes) {
        if (n.sx === undefined || n.sy === undefined) continue;
        const key = groupOf(n, by);
        if (!key || key === ungrouped) continue;
        const members = out.get(key);
        if (members) members.push(n);
        else out.set(key, [n]);
    }
    return out;
}

/**
 * How many nodes each group holds, whether or not the layout has placed them.
 *
 * The denominator behind a region's count. `regionMembers` reports what reached the screen, and a
 * boundary drawn around that set with no denominator implies the container is only that.
 */
export function groupSizes(nodes, by) {
    const out = new Map();
    if (!by || by === 'none') return out;
    const ungrouped = ungroupedName(by);

    for (const n of nodes) {
        const key = groupOf(n, by);
        if (!key || key === ungrouped) continue;
        out.set(key, (out.get(key) || 0) + 1);
    }
    return out;
}

/**
 * How far a boundary sits outside its members.
 *
 * A share of their own extent rather than a fixed number of screen pixels. A fixed gap is a
 * different thing at every zoom: the nodes shrink as the reader zooms out and the gap does not, so
 * the boundary ends up almost entirely margin with the cluster a speck inside it. The floor keeps a
 * group of one drawable and the ceiling stops a large account becoming a bubble.
 */
export function regionPadding(points, floor, ceiling) {
    let lo = Infinity, hi = -Infinity, top = Infinity, bottom = -Infinity;
    for (const p of points) {
        lo = Math.min(lo, p.sx); hi = Math.max(hi, p.sx);
        top = Math.min(top, p.sy); bottom = Math.max(bottom, p.sy);
    }
    const extent = Math.max(hi - lo, bottom - top);
    if (!(extent > 0)) return floor;
    return Math.min(ceiling, Math.max(floor, extent * PADDING_RATIO));
}

// Measured against what looks like a boundary rather than a halo: about a tenth of the cluster.
const PADDING_RATIO = 0.085;

/**
 * The convex hull of a set of placed nodes, by Andrew's monotone chain.
 *
 * Returned counter-clockwise. Fewer than three points has no hull worth the name and is returned
 * as given, because a group of one is the common case and still has to be drawable.
 */
export function hullOf(points) {
    const pts = points
        .map(p => ({ x: p.sx, y: p.sy }))
        .sort((a, b) => a.x - b.x || a.y - b.y);
    if (pts.length < 3) return pts;

    const half = source => {
        const chain = [];
        for (const p of source) {
            while (chain.length >= 2 && cross(chain[chain.length - 2], chain[chain.length - 1], p) <= 0) {
                chain.pop();
            }
            chain.push(p);
        }
        chain.pop();
        return chain;
    };
    return half(pts).concat(half([...pts].reverse()));
}

/** Twice the signed area, which is all the ordering and degeneracy checks need. */
export function hullArea(hull) {
    if (!hull || hull.length < 3) return 0;
    let sum = 0;
    for (let i = 0; i < hull.length; i++) {
        const a = hull[i], b = hull[(i + 1) % hull.length];
        sum += a.x * b.y - b.x * a.y;
    }
    return Math.abs(sum) / 2;
}

/**
 * Pushes the boundary outward so it clears the node shapes rather than cutting through them.
 *
 * Also what gives a degenerate hull a body: one member is a point and collinear members are a line,
 * and neither can be stroked as a region until it has been given width.
 */
export function inflate(hull, padding) {
    if (!hull.length) return [];
    if (hull.length === 1) return circle(hull[0], padding);
    if (hull.length === 2 || hullArea(hull) === 0) return capsule(hull, padding);

    const cx = hull.reduce((s, p) => s + p.x, 0) / hull.length;
    const cy = hull.reduce((s, p) => s + p.y, 0) / hull.length;
    return hull.map(p => {
        const dx = p.x - cx, dy = p.y - cy;
        const len = Math.hypot(dx, dy) || 1;
        return { x: p.x + (dx / len) * padding, y: p.y + (dy / len) * padding };
    });
}

function circle(center, radius, segments = 16) {
    const out = [];
    for (let i = 0; i < segments; i++) {
        const a = (i / segments) * Math.PI * 2;
        out.push({ x: center.x + Math.cos(a) * radius, y: center.y + Math.sin(a) * radius });
    }
    return out;
}

// A rounded band around the line the members lie on, which is what a two-member or collinear group
// is. Built from the extremes, so it survives however many points sit between them.
function capsule(hull, padding) {
    const a = hull[0], b = hull[hull.length - 1];
    const dx = b.x - a.x, dy = b.y - a.y;
    const len = Math.hypot(dx, dy);
    if (!len) return circle(a, padding);
    const nx = (-dy / len) * padding, ny = (dx / len) * padding;
    const ex = (dx / len) * padding, ey = (dy / len) * padding;
    return [
        { x: a.x + nx - ex, y: a.y + ny - ey },
        { x: b.x + nx + ex, y: b.y + ny + ey },
        { x: b.x - nx + ex, y: b.y - ny + ey },
        { x: a.x - nx - ex, y: a.y - ny - ey },
    ];
}

/**
 * Whether a boundary encloses a point, by ray casting.
 *
 * Half-open on the vertical span so a ray leaving through a vertex is counted once, which is the
 * case a naive crossing count gets wrong — and it gets it wrong in the reader's favour, reporting
 * a node as outside a boundary that does enclose it.
 */
export function containsPoint(polygon, point) {
    let inside = false;
    for (let i = 0, j = polygon.length - 1; i < polygon.length; j = i++) {
        const a = polygon[i], b = polygon[j];
        if ((a.y > point.y) === (b.y > point.y)) continue;
        const x = a.x + ((point.y - a.y) / (b.y - a.y)) * (b.x - a.x);
        if (point.x < x) inside = !inside;
    }
    return inside;
}

/**
 * The area two boundaries share, by Sutherland–Hodgman clipping.
 *
 * Both arguments must be convex, which everything `inflate` returns is. Overlap is the measurement
 * that says whether grouping is doing anything: two boundaries covering the same pixels assert two
 * containments over one set of nodes.
 */
export function overlapArea(subject, clip) {
    if (!subject || !clip || subject.length < 3 || clip.length < 3) return 0;
    const ccw = signedArea(clip) > 0;
    let out = subject;

    for (let i = 0; i < clip.length && out.length; i++) {
        const a = clip[i], b = clip[(i + 1) % clip.length];
        const inside = p => (ccw ? cross(a, b, p) >= 0 : cross(a, b, p) <= 0);
        const next = [];
        for (let j = 0; j < out.length; j++) {
            const cur = out[j], prev = out[(j + out.length - 1) % out.length];
            if (inside(cur)) {
                if (!inside(prev)) next.push(intersect(prev, cur, a, b));
                next.push(cur);
            } else if (inside(prev)) {
                next.push(intersect(prev, cur, a, b));
            }
        }
        out = next;
    }
    return hullArea(out);
}

function signedArea(poly) {
    let sum = 0;
    for (let i = 0; i < poly.length; i++) {
        const a = poly[i], b = poly[(i + 1) % poly.length];
        sum += a.x * b.y - b.x * a.y;
    }
    return sum / 2;
}

const cross = (o, a, b) => (a.x - o.x) * (b.y - o.y) - (a.y - o.y) * (b.x - o.x);

function intersect(p, q, a, b) {
    const d = (q.x - p.x) * (b.y - a.y) - (q.y - p.y) * (b.x - a.x);
    if (!d) return { x: q.x, y: q.y };
    const t = ((a.x - p.x) * (b.y - a.y) - (a.y - p.y) * (b.x - a.x)) / d;
    return { x: p.x + (q.x - p.x) * t, y: p.y + (q.y - p.y) * t };
}

/**
 * Members split into as few boundaries as enclose none of `foreign`.
 *
 * A region asserts containment, so it may not enclose a node that belongs to somebody else — and a
 * convex hull over a C-shaped cluster does exactly that to whatever sits in the mouth of the C.
 * Splitting at the widest gap along the cluster's longest axis separates the arms of the C, which
 * is the shape the force layout actually produces.
 *
 * A single member is the floor. Two nodes drawn within a padding of each other cannot be separated
 * by any boundary, so the rule gives up there rather than recursing forever.
 */
export function containedClusters(members, foreign, padding) {
    if (members.length < 2) return [members];
    const hull = inflate(hullOf(members), padding);
    if (!foreign.some(f => containsPoint(hull, { x: f.sx, y: f.sy }))) return [members];

    const [a, b] = splitAtWidestGap(members);
    if (!a.length || !b.length) return [members];
    return containedClusters(a, foreign, padding).concat(containedClusters(b, foreign, padding));
}

// Along the longer extent, because that is the axis the gap that made a C is on.
function splitAtWidestGap(members) {
    const xs = members.map(m => m.sx), ys = members.map(m => m.sy);
    const horizontal = Math.max(...xs) - Math.min(...xs) >= Math.max(...ys) - Math.min(...ys);
    const sorted = [...members].sort((p, q) => (horizontal ? p.sx - q.sx : p.sy - q.sy));

    let at = 1, widest = -1;
    for (let i = 1; i < sorted.length; i++) {
        const gap = horizontal ? sorted[i].sx - sorted[i - 1].sx : sorted[i].sy - sorted[i - 1].sy;
        if (gap > widest) {
            widest = gap;
            at = i;
        }
    }
    return [sorted.slice(0, at), sorted.slice(at)];
}

