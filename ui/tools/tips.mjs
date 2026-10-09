// Every column header and every action or link says, in a sentence, what it holds or what it does.
//
// A column name is not a definition, and a button's label is not a description of what pressing
// it changes. Headers carry a HeaderTip, which keeps the grid's sort; actions and links carry a
// title, which adds no element around them and so cannot change how they lay out, or a Tip, which
// shows at once where a title shows late or never. One already inside a MudTooltip, HeaderTip or
// Tip is described by it, and a title on it as well would show a second tip.

const COLUMN = /<(PropertyColumn|TemplateColumn)(?=[\s>/])/g;
const HEADER_CELL = /<(th|MudTh)(?=[\s>/])/g;
const ACTION = /<(MudButton|MudIconButton|MudLink|MudMenuItem|MudChip|a|button)(?=[\s>/])/g;
const TOOLTIP = /<(\/?)(MudTooltip|HeaderTip|Tip)(?=[\s>/])/g;

/**
 * Where the opening tag starting at `start` ends, just past its `>`. Attribute values are skipped
 * whole, including a Razor `@(...)` whose C# may hold arrows, quotes and its own `>`.
 */
export function tagEnd(text, start) {
    let i = start;
    while (i < text.length) {
        const c = text[i];
        if (c === '>') return i + 1;
        if (c === '"') {
            i = valueEnd(text, i + 1);
            continue;
        }
        i++;
    }
    return text.length;
}

// Just past the quote closing an attribute value that starts at `i`.
function valueEnd(text, i) {
    if (text.startsWith('@(', i)) i = expressionEnd(text, i + 2);
    const close = text.indexOf('"', i);
    return close < 0 ? text.length : close + 1;
}

// Just past the `)` balancing an expression whose body starts at `i`, outside C# strings.
function expressionEnd(text, i) {
    let depth = 1;
    let inString = false;
    for (; i < text.length && depth > 0; i++) {
        const c = text[i];
        if (inString) {
            if (c === '\\') i++;
            else if (c === '"') inString = false;
        } else if (c === '"') inString = true;
        else if (c === '(') depth++;
        else if (c === ')') depth--;
    }
    return i;
}

const lineOf = (text, index) => text.slice(0, index).split('\n').length;

// A literal attribute value, or null where it is absent or computed.
function literal(tag, name) {
    const m = tag.match(new RegExp(`\\s${name}="`));
    if (!m) return null;
    const start = m.index + m[0].length;
    if (tag[start] === '@' && tag[start + 1] !== '@') return null;
    const value = tag.slice(start, tag.indexOf('"', start)).replaceAll('@@', '@');
    return value.includes('@') ? null : value;
}

// An attribute that is one interpolated string, as written and with each hole read as a word; null
// for anything else, whose wording only running it would show.
function interpolated(tag, name) {
    const m = tag.match(new RegExp(`\\s${name}="@\\(`));
    if (!m) return null;
    const start = m.index + m[0].length;
    const expression = tag.slice(start, expressionEnd(tag, start) - 1).trim();
    if (!expression.startsWith('$"') || !expression.endsWith('"')) return null;
    const shown = expression.slice(2, -1);
    let read = '';
    let depth = 0;
    for (const c of shown) {
        if (c === '{') read += depth++ === 0 ? 'X' : '';
        else if (c === '}') depth--;
        else if (depth === 0) read += c;
    }
    return { shown, read };
}

const isSentence = (s) =>
    /^[A-Z0-9"'`]/.test(s) && /[.?!]$/.test(s.trim()) && s.trim().split(/\s+/).length >= 3;

// The spans a MudTooltip or HeaderTip covers, so anything inside them is already described.
function tooltipSpans(text) {
    const spans = [];
    const open = [];
    for (const m of text.matchAll(TOOLTIP)) {
        const end = tagEnd(text, m.index);
        if (m[1] === '/') {
            const start = open.pop();
            if (start !== undefined) spans.push([start, end]);
        } else if (text[end - 2] !== '/') {
            open.push(m.index);
        }
    }
    return spans;
}

/** Each place a header or an action says nothing of what it is, as `{ line, message }`. */
export function untipped(text) {
    const problems = [];
    const add = (index, message) => problems.push({ line: lineOf(text, index), message });

    for (const m of text.matchAll(COLUMN)) {
        const end = tagEnd(text, m.index);
        const tag = text.slice(m.index, end);
        const title = tag.match(/\sTitle="([^"]*)"/)?.[1];
        if (!title) continue;
        const close = tag.endsWith('/>') ? end : text.indexOf(`</${m[1]}>`, end);
        if (!text.slice(end, close).includes('<HeaderTip')) {
            add(m.index, `the "${title}" column header has no HeaderTip`);
        }
    }

    for (const m of text.matchAll(HEADER_CELL)) {
        const end = tagEnd(text, m.index);
        const inner = text.slice(end, text.indexOf(`</${m[1]}>`, end));
        // The text between tags, for the message; nothing here is sanitized or rendered.
        const words = inner.split(/<[^>]*>/).join('').trim();
        if (words && !inner.includes('<HeaderTip')) {
            add(m.index, `the "${words}" header cell has no HeaderTip`);
        }
    }

    const spans = tooltipSpans(text);
    for (const m of text.matchAll(ACTION)) {
        const tag = text.slice(m.index, tagEnd(text, m.index));
        if (m[1] === 'MudChip' && !/\s(OnClick|Href)=/.test(tag)) continue;
        const titled = /\stitle="/.test(tag);
        const inside = spans.some(([s, e]) => m.index > s && m.index < e);
        if (!titled && !inside) add(m.index, `${m[1]} has no title or tooltip saying what it does`);
        if (titled && inside) add(m.index, `${m[1]} has a title inside a tooltip, so two tips show`);
    }

    for (const [pattern, attribute] of [[ACTION, 'title'], [TOOLTIP, 'Text'], [TOOLTIP, 'Tip']]) {
        for (const m of text.matchAll(pattern)) {
            const tag = text.slice(m.index, tagEnd(text, m.index));
            const plain = literal(tag, attribute);
            const { shown, read } = plain !== null ? { shown: plain, read: plain } : interpolated(tag, attribute) ?? {};
            if (read !== undefined && !isSentence(read)) add(m.index, `tip "${shown}" is not a sentence`);
        }
    }

    return problems.sort((a, b) => a.line - b.line);
}

/** Template columns whose `SortBy` MudBlazor ignores, because nothing set `Sortable="true"`. */
export function unsortable(text) {
    const problems = [];
    for (const m of text.matchAll(/<TemplateColumn(?=[\s>/])/g)) {
        const tag = text.slice(m.index, tagEnd(text, m.index));
        if (/\sSortBy=/.test(tag) && !/\sSortable="true"/.test(tag)) {
            const title = tag.match(/\sTitle="([^"]*)"/)?.[1] ?? '';
            problems.push({
                line: lineOf(text, m.index),
                message: `the "${title}" column has a SortBy but no Sortable="true", so it cannot be sorted`,
            });
        }
    }
    return problems;
}
