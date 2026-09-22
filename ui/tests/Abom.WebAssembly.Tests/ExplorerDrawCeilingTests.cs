using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A picture stops carrying information long before it stops rendering.
///
/// <para>Past a few hundred nodes a force-directed graph is a hairball: boundaries interleave and
/// are refused, labels have nowhere to go, and no engine on offer produces a readable shape. The
/// view refuses rather than warns, because a caution printed over a drawn hairball is a note
/// nobody reads beside a picture they are already trying to interpret. What the reader is offered
/// instead is the way to a scope worth drawing.</para>
/// </summary>
public sealed class ExplorerDrawCeilingTests : AbomTestContext
{
    public ExplorerDrawCeilingTests() => ArriveWithAScope();
    /// The number is the whole point of the refusal: "too big" tells a reader nothing about how
    /// much narrowing is owed, and 312 is a different problem from 12,725.
    [Fact]
    public void AGraphPastTheCeilingIsRefusedAndSaysHowLargeItIs()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(new Explorer.Counts(
            Nodes: 3703, Edges: 10086, Hidden: 0, Total: 3703, Withheld: 3703));

        var page = Render<Explorer>();

        var refusal = Assert.Single(page.FindAll("[data-abom-draw-refused]"));
        Assert.Contains("3,703", refusal.TextContent);
    }

    /// A refusal that only says no is an obstacle. The reader came to find something, and the
    /// route to it is narrowing rather than a bigger canvas.
    [Fact]
    public void TheRefusalPointsAtNarrowingRatherThanOnlyRefusing()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(new Explorer.Counts(
            Nodes: 3703, Edges: 10086, Hidden: 0, Total: 3703, Withheld: 3703));

        var page = Render<Explorer>();

        var refusal = page.Find("[data-abom-draw-refused]");
        Assert.Contains("filter", refusal.TextContent, System.StringComparison.OrdinalIgnoreCase);
    }

    /// Drawing one anyway is the reader's call to make, and it is a deliberate one: the override
    /// is a dialog rather than a second click on the canvas.
    [Fact]
    public void DrawingItAnywayIsOfferedButNotTakenWithoutConfirming()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(new Explorer.Counts(
            Nodes: 3703, Edges: 10086, Hidden: 0, Total: 3703, Withheld: 3703));

        var provider = Render<MudBlazor.MudDialogProvider>();
        var page = Render<Explorer>();

        // Asserted before the press, or the check below passes on markup that was always there
        // and the "explicit approval" it is protecting is not being tested at all.
        Assert.DoesNotContain("data-abom-draw-warning", page.Markup + provider.Markup);

        page.Find("[data-abom-draw-anyway]").Click();

        var markup = page.Markup + provider.Markup;
        Assert.Contains("data-abom-draw-warning", markup);
        Assert.Contains("3,703", markup);
        // The warning has to say what the reader is trading, not merely ask twice.
        Assert.Contains("unreadable", markup, System.StringComparison.OrdinalIgnoreCase);
    }

    /// Below the ceiling nothing is withheld, so nothing is said: a refusal panel on an ordinary
    /// view would train the reader to click past it.
    [Fact]
    public void AGraphUnderTheCeilingDrawsWithNoRefusal()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(new Explorer.Counts(
            Nodes: 120, Edges: 180, Hidden: 0, Total: 120, Withheld: 0));

        var page = Render<Explorer>();

        Assert.Empty(page.FindAll("[data-abom-draw-refused]"));
    }

    private void StubGraphModule(Explorer.Counts? counts = null)
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(counts ?? new Explorer.Counts(0, 0, 0, 0));
        module.Setup<Explorer.Counts>("approveLargeGraph", _ => true)
              .SetResult(counts ?? new Explorer.Counts(0, 0, 0, 0));
        module.Setup<Explorer.LayoutState>("applyServerLayout", _ => true)
              .SetResult(new Explorer.LayoutState(false, "Force layout", 0, 0));
        module.SetupVoid("clearServerLayout", _ => true).SetVoidResult();
        module.SetupVoid("setPalette", _ => true).SetVoidResult();
        module.SetupVoid("setAnimate", _ => true).SetVoidResult();
        module.SetupVoid("setGroupBy", _ => true).SetVoidResult();
        module.SetupVoid("setCollapsed", _ => true).SetVoidResult();
        module.SetupVoid("setView", _ => true).SetVoidResult();
        module.SetupVoid("setGeneration", _ => true).SetVoidResult();
        module.SetupVoid("setSelection", _ => true).SetVoidResult();
        module.SetupVoid("resize", _ => true).SetVoidResult();
        module.SetupVoid("rebuildNodes", _ => true).SetVoidResult();
    }
}
