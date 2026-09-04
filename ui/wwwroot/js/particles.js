// Direction markers along the selection's edges.

import { keyOf, seed, touchesAny } from './topology.js';
import { instances } from './state.js';

/**
 * A distinct speed per edge, so the particles do not beat in unison.
 *
 * Every link starts its particles at the same phase and there is no API to offset them. Sharing
 * one speed therefore kept them in lockstep, and a hub's worth of edges read as a single pulse
 * arriving every cycle. The slowest edge here takes roughly four times as long to traverse as
 * the fastest, which pulls them apart and keeps them apart. Seeded by the edge itself, so the
 * same graph always animates the same way.
 *
 * Kept slow deliberately: this marks direction on the selection, and anything quick enough to
 * catch the eye competes with the graph for attention.
 */
export function particleSpeed(link) {
    return 0.0004 + seed(`${keyOf(link.source)}>${keyOf(link.target)}#p`) * 0.0014;
}

/** Share of a selection's edges carrying a particle during any one phase. */
const PARTICLE_SHARE = 0.14;

const PARTICLE_PHASE_MS = 6000;

/**
 * Only a slice of the selection's edges animates at a time.
 *
 * force-graph restarts a link's particles the instant they arrive, so a link that animates at
 * all animates constantly and there is no API for a gap. Rotating a small random subset gives
 * each edge long stretches of silence, which is what turns a pulse into occasional movement.
 */
export function particleCount(it, link) {
    if (!it.animate || !it.selected || !it.selected.size) return 0;
    if (!touchesAny(link, it.selected)) return 0;
    const k = `${keyOf(link.source)}>${keyOf(link.target)}#${it.particlePhase || 0}`;
    return seed(k) < PARTICLE_SHARE ? 1 : 0;
}

export function startParticleCycle(it) {
    clearInterval(it.particleTimer);
    if (!it.selected || !it.selected.size || !it.animate) return;
    it.particleTimer = setInterval(() => {
        const cur = instances.get(it.id);
        if (!cur || !cur.running) return;
        cur.particlePhase = (cur.particlePhase || 0) + 1;
        // Reassigning the accessor is what makes force-graph re-evaluate the active subset.
        cur.graph.linkDirectionalParticles(l => particleCount(cur, l));
    }, PARTICLE_PHASE_MS);
}
