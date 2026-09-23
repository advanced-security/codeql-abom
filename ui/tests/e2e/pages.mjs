// The page and part an address shows, as test tags; the one mapping both tagging and selection read.

// Which query parameter names a part of a page, and the part a bare address opens on.
const PARTS = {
    codeql: { param: 'tab', opens: 'requests' },
    inventory: { param: 'unit', opens: 'dependencies' },
};

function pageOf(pathname) {
    return pathname === '/' ? 'home' : pathname.split('/')[1];
}

/** The tags for a test that visits `path`: its page, and the part it shows if the page has parts. */
export function tagsOf(path) {
    const url = new URL(path, 'http://abom.invalid');
    const page = pageOf(url.pathname);
    const parts = PARTS[page];
    if (!parts) return [`@${page}`];
    return [`@${page}`, `@${page}:${url.searchParams.get(parts.param) ?? parts.opens}`];
}

/** Selects tests by page or part, each named as an address (`/codeql?tab=runs`) or a tag (`codeql:runs`). */
export function grepFor(names) {
    const tags = names.map((name) => {
        if (!name.startsWith('/')) return name.startsWith('@') ? name : `@${name}`;
        const url = new URL(name, 'http://abom.invalid');
        const [page, part] = tagsOf(name);
        return url.searchParams.has(PARTS[pageOf(url.pathname)]?.param) ? part : page;
    });
    const escaped = tags.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
    return new RegExp(`(?:^|\\s)(?:${escaped.join('|')})(?:\\s|$)`);
}
