// Fails when the interface starts growing a second visual vocabulary.
//
// Every rule here corresponds to drift that had already happened: six font sizes between
// 0.68rem and 0.8rem, nine page headings in three shapes, two search widths, and forty-nine
// inline style declarations. Review caught none of it, which is why this is a build step.
//
// Exceptions are marked in the source with `style-check: off` / `style-check: on` rather than
// collected in a list of line numbers, so an exception states its reason where it applies and
// cannot rot when a file shifts by a line.

import { readdir, readFile } from 'node:fs/promises';
import { join, relative } from 'node:path';
import { unsortable, untipped } from './tips.mjs';

const root = new URL('..', import.meta.url).pathname;
const problems = [];

function report(file, line, message) {
    problems.push(`${file}:${line}\n    ${message}`);
}

/** Lines inside a `style-check: off` block, which must say why they are exempt. */
function suppressed(text) {
    const off = new Set();
    let disabled = false;
    text.split('\n').forEach((line, i) => {
        if (/style-check:\s*off/.test(line)) disabled = true;
        if (disabled) off.add(i + 1);
        if (/style-check:\s*on/.test(line)) disabled = false;
    });
    return off;
}

/**
 * A style attribute is invisible to the theme and cannot be overridden.
 *
 * One exception needs no marking: an attribute that sets only custom properties is handing a
 * live palette value to CSS, which is the one thing a stylesheet cannot do for itself.
 */
function checkInlineStyles(file, text, off) {
    const re = /\bstyle="([^"]*)"/gi;
    for (const m of text.matchAll(re)) {
        const line = text.slice(0, m.index).split('\n').length;
        if (off.has(line)) continue;
        const body = m[1].trim();
        const onlyCustomProperties = body.length > 0 &&
            body.split(';').filter(Boolean).every(d => d.trim().startsWith('--'));
        if (!onlyCustomProperties) {
            report(file, line, 'inline style; use a class in wwwroot/css/app.css');
        }
    }
}

/** A literal color bypasses the palette, so it cannot follow the theme. */
function checkColors(file, text, off) {
    text.split('\n').forEach((line, i) => {
        if (off.has(i + 1)) return;
        // `&#8594;` is a character reference, not a color.
        const withoutEntities = line.replace(/&#\w+;/g, '');
        if (/#[0-9a-fA-F]{3,8}\b/.test(withoutEntities) ||
            /\brgba?\(|\bhsla?\(/.test(withoutEntities)) {
            report(file, i + 1, 'hard-coded color; use the palette or a token');
        }
    });
}

/** A size written as a literal is a seventh size nobody chose. */
function checkFontSizes(file, text, off) {
    text.split('\n').forEach((line, i) => {
        if (off.has(i + 1)) return;
        // Only absolute units. `em` is relative to whatever the scale already set, so inline
        // code sized at 0.95em still follows the type it sits in.
        if (/font-size:\s*[0-9.]+(rem|px|pt)\b/.test(line)) {
            report(file, i + 1, 'literal font-size; use var(--abom-text-*)');
        }
    });
}

/**
 * One word per notion. `origin` records how a repository entered the corpus, which is provenance
 * of the data; rendering it as "in scope" or "under review" turns it into a claim that somebody
 * is responsible for those repositories, and nothing about ABOM happens until an operator
 * declares ownership. Boundary classes had three spellings across three surfaces, so a reader
 * filtering "Another account" on one page and "Cross org/owner" on another could not tell they
 * had asked the same question.
 */
const retiredWords = [
    [/\bin scope\b/i, 'says "in scope"; ownership is declared, so say whose it is or say "reached"'],
    [/\bunder review\b/i, 'says "under review"; no repository is under review by nobody'],
    [/\bSame org\/owner\b/, 'boundary wording; the interface says "same account"'],
    [/\bCross org\/owner\b/, 'boundary wording; the interface says "another account"'],
    [/\bSame repo\b(?!sitory)/, 'boundary wording; the interface says "same repository"'],
    // A reader rarely controls an upstream tag; what they can ask is what its moving would change.
    [/\bIf you bump this\b/i, 'says "If you bump this"; the section is "If this tag changes"'],
    // Only inside a quoted string, because the word is ordinary English in a comment about one.
    // It reached the Dashboard as "Advisories in the corpus" and was read as jargon, which it is.
    [
        /"[^"]*\bcorpus\b/i,
        'says "corpus" in a string a reader sees; say "recorded", "in the graph" or name the thing',
    ],
];

function checkVocabulary(file, text, off) {
    text.split('\n').forEach((line, i) => {
        if (off.has(i + 1)) return;
        for (const [re, message] of retiredWords) {
            if (re.test(line)) report(file, i + 1, message);
        }
    });
}

/**
 * A reader met `view: this answer applies no "boundary"` on the Explorer. `view:` is the Go
 * package the refusal was written in, and the browser's parser mirrors its wording, so the same
 * stack frame reached the screen from both sides.
 *
 * Only inside a quoted string: `view:` is ordinary English in a doc comment about a view.
 */
const machineText = [
    [/"[^"]*\bview:/, 'a refusal carrying "view:"; that is a Go package name, not a sentence'],
];

function checkMachineText(file, text, off) {
    text.split('\n').forEach((line, i) => {
        if (off.has(i + 1)) return;
        for (const [re, message] of machineText) {
            if (re.test(line)) report(file, i + 1, message);
        }
    });
}

/** Headings belong to PageHeader, which is what keeps the document outline stable. */
function checkHeadings(file, text, off) {    text.split('\n').forEach((line, i) => {
        if (off.has(i + 1) || file.endsWith('PageHeader.razor')) return;
        if (/<h1[\s>]/.test(line)) {
            report(file, i + 1, 'page heading outside PageHeader');
        }
    });
}

/**
 * A count tile: a figure set as a heading, in a box, with a caption and nowhere to go.
 *
 * Every page grew a row of them and every one was stripped out again. Each repeated a number a
 * section beneath it already carried, linked nowhere, and pushed the first useful row down the
 * screen. A figure belongs in the sentence or the column it qualifies. A heading-sized figure that
 * is itself the way into a scope must say so inside a `style-check: off` block.
 */
function checkCountTiles(file, text, off) {
    // A heading-sized element that is not a heading and whose whole text is one computed value.
    const re = /<MudText\b([^>]*)>([^<]*)<\/MudText>/g;
    for (const m of text.matchAll(re)) {
        const line = text.slice(0, m.index).split('\n').length;
        if (off.has(line)) continue;
        const [, attributes, body] = m;
        if (!/Typo="Typo\.h[1-6]"/.test(attributes) || /HtmlTag="h[1-6]"/.test(attributes)) continue;
        if (/\babom-num\b/.test(attributes) || /^\s*@[\w.(]/.test(body) && !/\s\w+\s/.test(body)) {
            report(file, line, 'count tile; put the figure in the sentence or column it qualifies');
        }
    }
    for (const m of text.matchAll(/<MetricTile\b/g)) {
        const line = text.slice(0, m.index).split('\n').length;
        if (!off.has(line)) report(file, line, 'count tile; MetricTile was removed on purpose');
    }
}

/** Every header and every action or link says what it is; the rule lives in tips.mjs. */
function checkTips(file, text, off) {
    for (const { line, message } of [...untipped(text), ...unsortable(text)]) {
        if (!off.has(line)) report(file, line, message);
    }
}

/** An icon with no size renders at Medium among a page of Small ones. */
function checkIconSizes(file, text, off) {
    // The attribute is often on a continuation line, so the element is read whole.
    const re = /<MudIcon\b[\s\S]*?\/?>/g;
    for (const m of text.matchAll(re)) {
        const line = text.slice(0, m.index).split('\n').length;
        if (off.has(line)) continue;
        if (!/\bSize=/.test(m[0])) {
            report(file, line, 'MudIcon without Size');
        }
    }
}

async function* walk(dir) {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) {
            if (['bin', 'obj', 'node_modules'].includes(entry.name)) continue;
            yield* walk(path);
        } else {
            yield path;
        }
    }
}

console.log('checking interface style...');

// `Services` is here for one reason: its refusal strings are rendered verbatim, so a message
// written for a log reaches a reader unchanged.
for (const dir of ['Pages', 'Shared', 'Layout', 'Services', 'wwwroot/css']) {
    for await (const path of walk(join(root, dir))) {
        if (!/\.(razor|css|cs)$/.test(path)) continue;
        const file = relative(root, path);
        const text = await readFile(path, 'utf8');
        const off = suppressed(text);

        checkMachineText(file, text, off);
        if (path.endsWith('.cs')) continue;

        checkColors(file, text, off);
        checkFontSizes(file, text, off);
        if (path.endsWith('.razor')) {
            checkInlineStyles(file, text, off);
            checkHeadings(file, text, off);
            checkIconSizes(file, text, off);
            checkVocabulary(file, text, off);
            checkCountTiles(file, text, off);
            checkTips(file, text, off);
        }
    }
}

if (problems.length > 0) {
    for (const p of problems.sort()) console.log('  ' + p);
    console.log(`style: ${problems.length} problems. Fix them, or mark the block ` +
        '`style-check: off` with the reason it cannot follow the rule.');
    process.exit(1);
}
console.log('style: clean');
