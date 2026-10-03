import { test } from 'node:test';
import assert from 'node:assert/strict';
import { unsortable, untipped } from './tips.mjs';

const messages = (text) => untipped(text).map((p) => `${p.line}: ${p.message}`);

test('a titled column needs a header tip, and an untitled one does not', () => {
    const text = [
        '<PropertyColumn Property="x => x.A" Title="Name" />',
        '<PropertyColumn Property="x => x.B" Title="Type">',
        '    <CellTemplate>@context.Item.B</CellTemplate>',
        '</PropertyColumn>',
        '<TemplateColumn Title="Kind">',
        '    <HeaderTemplate><HeaderTip Tip="What kind of thing this row is.">Kind</HeaderTip></HeaderTemplate>',
        '</TemplateColumn>',
        '<TemplateColumn Title="" Sortable="false"><CellTemplate>x</CellTemplate></TemplateColumn>',
    ].join('\n');

    assert.deepEqual(messages(text), [
        '1: the "Name" column header has no HeaderTip',
        '2: the "Type" column header has no HeaderTip',
    ]);
});

test('a header cell with text needs a tip, and an empty one does not', () => {
    const text = [
        '<tr><th>Dependency</th><th></th>',
        '<th><HeaderTip Tip="How many lines name it here.">Lines</HeaderTip></th>',
        '<th class="x"><MudCheckBox T="bool" /></th></tr>',
    ].join('\n');

    assert.deepEqual(messages(text), ['1: the "Dependency" header cell has no HeaderTip']);
});

test('an action or a link needs a title or an enclosing tooltip', () => {
    const text = [
        '<MudButton OnClick="Go">Go</MudButton>',
        '<MudIconButton Icon="@Icons.Close" title="Close this panel." />',
        '<MudTooltip Text="Run the queries over this scope.">',
        '    <MudButton OnClick="Run">Run</MudButton>',
        '</MudTooltip>',
        '<a href="/x">x</a>',
        '<MudLink Href="/y" title="@($"Open the details of {name}.")">y</MudLink>',
        '<button type="button" @onclick="Go">z</button>',
    ].join('\n');

    assert.deepEqual(messages(text), [
        '1: MudButton has no title or tooltip saying what it does',
        '6: a has no title or tooltip saying what it does',
        '8: button has no title or tooltip saying what it does',
    ]);
});

test('an opening tag is read past arrows and quotes nested in an expression', () => {
    const text = [
        '<MudIconButton OnClick="@(() => Toggle(item))" aria-label="@($"Show the versions of {key}")"',
        '               title="Show the versions in use." />',
        '<MudIconButton OnClick="@(() => Close("a>b"))" aria-label="@($"Close {key}")" />',
    ].join('\n');

    assert.deepEqual(messages(text), ['3: MudIconButton has no title or tooltip saying what it does']);
});

test('a chip is an action only when it does something', () => {
    const text = [
        '<MudChip T="string" Text="static" />',
        '<MudChip T="string" Text="click" OnClick="Pick" />',
        '<MudChip T="string" Text="link" Href="/z" title="Open the z view." />',
    ].join('\n');

    assert.deepEqual(messages(text), ['2: MudChip has no title or tooltip saying what it does']);
});

test('elements that only start with a tag name are not anchors or buttons', () => {
    const text = '<abbr title="x">x</abbr><article>y</article><MudButtonGroup>z</MudButtonGroup>';

    assert.deepEqual(messages(text), []);
});

test('a literal tip is a sentence, and an expression is not judged', () => {
    const text = [
        '<MudButton title="open it">Open</MudButton>',
        '<MudTooltip Text="Upstream count"><span>x</span></MudTooltip>',
        '<HeaderTip Tip="Names it.">Name</HeaderTip>',
        '<MudLink Href="/a" title="The version written after the @@ in a uses: line.">a</MudLink>',
        '<MudLink Href="/b" title="@Describe(item)">b</MudLink>',
    ].join('\n');

    assert.deepEqual(messages(text), [
        '1: tip "open it" is not a sentence',
        '2: tip "Upstream count" is not a sentence',
        '3: tip "Names it." is not a sentence',
    ]);
});

test('an interpolated tip is a sentence too, with each hole read as a word', () => {
    const text = [
        '<MudIconButton title="@($"Forget {view.Label}")" />',
        '<MudLink Href="/n" title="@($"Open the details page for {n.Key}.")">n</MudLink>',
        '<MudButton title="@(open ? "Hide it." : "Show it.")">x</MudButton>',
    ].join('\n');

    assert.deepEqual(messages(text), ['1: tip "Forget {view.Label}" is not a sentence']);
});

test('a template column given a sort key also says it sorts, or the key is silently ignored', () => {
    const text = [
        '<TemplateColumn Title="Kind" SortBy="x => x.Kind">',
        '<TemplateColumn Title="Findings" Sortable="true" SortBy="FindingsOrder">',
        '<TemplateColumn Title="" Sortable="false">',
        '<PropertyColumn Property="x => x.A" Title="A" />',
    ].join('\n');

    assert.deepEqual(unsortable(text).map((p) => `${p.line}: ${p.message}`),
        ['1: the "Kind" column has a SortBy but no Sortable="true", so it cannot be sorted']);
});
