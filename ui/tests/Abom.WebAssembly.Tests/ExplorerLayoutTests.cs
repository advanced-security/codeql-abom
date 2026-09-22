using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// What a reader is told about the positions in front of them.
///
/// <para>The force simulation has no fixed seed, so no two runs agree and nothing downstream can
/// rest on a position. A view that claims its positions carry meaning, while a simulation decides
/// them afresh on every load, is asserting something the picture does not hold — and nothing else
/// on the page distinguishes a layout that will be identical tomorrow from one that will not.</para>
/// </summary>
public sealed class ExplorerLayoutTests : AbomTestContext
{
    public ExplorerLayoutTests() => ArriveWithAScope();
    /// The Neighborhood view is the one whose position comes from the simulation with no semantic
    /// force behind it. Priority, Coverage and Distance earn their radial claims; this one did not,
    /// and said so anyway.
    [Fact]
    public void TheNeighborhoodViewDoesNotClaimAMeaningTheSimulationDoesNotCarry()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(new Explorer.Counts(Nodes: 12, Edges: 4, Hidden: 0, Total: 12));

        var page = Render<Explorer>();

        var neighborhood = Assert.Single(
            page.FindAll("[data-abom-view='neighborhood']"));
        Assert.DoesNotContain("Position is structural", neighborhood.GetAttribute("title"));
    }

    /// A reader looking at a stable layout and a reader looking at a force simulation are looking
    /// at two different kinds of claim, and the control is the only thing that says which.
    [Fact]
    public void ThePageStatesWhichLayoutDrewThePicture()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(new Explorer.Counts(Nodes: 12, Edges: 4, Hidden: 0, Total: 12));

        var page = Render<Explorer>();

        var state = Assert.Single(page.FindAll("[data-abom-layout-state]"));
        Assert.Contains("every reload", state.TextContent);
    }

    /// Graphviz is optional, and a canvas that went blank without it would make a missing tool
    /// look like a broken page. The refusal is named rather than swallowed.
    [Fact]
    public void ALayoutTheEngineCouldNotComputeNamesItsReason()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(
            new Explorer.Counts(Nodes: 12, Edges: 4, Hidden: 0, Total: 12),
            new Explorer.LayoutState(
                Stable: false, Text: "Force layout — layout engine is not installed: dot",
                Pinned: 0, Free: 0));

        var page = Render<Explorer>();
        page.Find("[data-abom-layout-toggle] input").Change(true);

        var state = Assert.Single(page.FindAll("[data-abom-layout-state]"));
        Assert.Contains("not installed", state.TextContent);
    }

    /// A partial layout is a partial claim, and "dot layout" over a picture the simulation drew a
    /// quarter of tells the reader the whole thing is reproducible.
    [Fact]
    public void AStableLayoutReportsHowMuchOfItTheSimulationStillDrew()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(
            new Explorer.Counts(Nodes: 12, Edges: 4, Hidden: 0, Total: 12),
            new Explorer.LayoutState(
                Stable: true, Text: "dot layout — 2 of 12 placed by the simulation",
                Pinned: 10, Free: 2));

        var page = Render<Explorer>();
        page.Find("[data-abom-layout-toggle] input").Change(true);

        var state = Assert.Single(page.FindAll("[data-abom-layout-state]"));
        Assert.Contains("2 of 12 placed by the simulation", state.TextContent);
    }

    private void StubGraphModule(
        Explorer.Counts? counts = null, Explorer.LayoutState? layout = null)
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(counts ?? new Explorer.Counts(0, 0, 0, 0));
        module.Setup<Explorer.LayoutState>("applyServerLayout", _ => true)
              .SetResult(layout ?? new Explorer.LayoutState(false, "Force layout", 0, 0));
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
