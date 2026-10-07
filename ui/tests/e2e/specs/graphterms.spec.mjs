import { test, expect, ready, corpusSize, on } from '../harness.mjs';

// The graph applies these terms when it is sent them, and the Explorer applies them itself over
// one payload so an interaction costs no round trip. Two derivations of one answer disagree
// silently, so each is compared per term: one agreeing proves nothing about the others.
const BOTH_SIDES = [
    'origins=seed', 'origins=upstream', 'origins=external',
    'minReach=2', 'minReach=5', 'ref=sha', 'ref=tag', 'flags=unanalyzed',
];

/** What the Explorer answered, as opposed to what it drew only so a relationship keeps both ends. */
async function browserAnswers(page) {
    return page.evaluate(async () => {
        const { instances } = await import('/js/state.js');
        const it = instances.get('abom-graph');
        if (!it || !it.all || !it.visible || !it.contextKeys) return null;
        return [...it.visible].filter((k) => !it.contextKeys.has(k)).sort();
    });
}

test('the graph and the Explorer select the same nodes for every term both apply', on('/explore'),
    async ({ page, request, baseURL }) => {
        test.slow();
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.nodes, 'nothing to select');

        for (const term of BOTH_SIDES) {
            const res = await request.get(`${baseURL}/api/graph/full?scope=supply-chain&${term}`);
            expect(res.ok(), `the graph answered ${term}`).toBeTruthy();
            const server = (await res.json()).nodes.map((n) => n.key).sort();

            await page.goto(`/explore?scope=supply-chain&${term}`);
            await ready(page);
            await expect.poll(() => browserAnswers(page), { timeout: 30_000, message: term })
                .not.toBeNull();
            const browser = await browserAnswers(page);

            const onlyServer = server.filter((k) => !browser.includes(k));
            const onlyBrowser = browser.filter((k) => !server.includes(k));
            expect({ term, onlyServer, onlyBrowser }).toEqual({ term, onlyServer: [], onlyBrowser: [] });
        }
    });

// A band is handed to the browser and read from each finding's facet, so what it keeps has to be
// what the set path keeps: every workflow the set path names under the band is drawn as itself,
// and everything the browser keeps holds a finding in that band.
const BANDS = ['securitySeverity=critical', 'securitySeverity=critical%2Chigh', 'securitySeverity=unknown'];

test('the Explorer keeps what the set path keeps for a band', on('/explore'),
    async ({ page, request, baseURL }) => {
        test.slow();
        const summary = await corpusSize(request, baseURL);
        test.skip(!summary.nodes, 'nothing to select');

        const full = await (await request.get(`${baseURL}/api/graph/full?scope=supply-chain`)).json();
        const drawn = new Map(full.nodes.map((n) => [n.key, n]));
        for (const term of BANDS) {
            const bands = decodeURIComponent(term.split('=')[1]).split(',');
            const match = await (await request.get(`${baseURL}/api/match?${term}`)).json();
            const named = match.nodes.map((n) => n.key).filter((k) => drawn.has(k));

            await page.goto(`/explore?scope=supply-chain&${term}`);
            await ready(page);
            await expect.poll(() => browserAnswers(page), { timeout: 30_000, message: term })
                .not.toBeNull();
            const browser = await browserAnswers(page);

            const missed = named.filter((k) => !browser.includes(k));
            const unbanded = browser.filter((k) =>
                !(drawn.get(k)?.facets || []).some((f) => bands.includes(f.band)));
            expect({ term, missed, unbanded }).toEqual({ term, missed: [], unbanded: [] });
        }
    });
