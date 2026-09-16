using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Selection lives in two places at once: <c>_isolated</c> and <c>_selected</c> in C#, and
/// <c>it.selected</c> in the graph module. Every escape hatch has to route through both, and a
/// half-applied clear leaves the page saying one thing and the canvas showing another.
///
/// <para>These pin the routing and the two decisions that are invisible from the markup: which
/// acts earn a camera move, and which do nothing at all.</para>
/// </summary>
public sealed class ExplorerSelectionTests : AbomTestContext
{
    public ExplorerSelectionTests()
    {
        Answer("api/graph/generation", "{\"generation\":0}");
        Answer("api/summary", "{}");
    }

    /// <summary>
    /// A single click highlights and nothing more. Rebuilding the view is a large enough change
    /// that it takes a deliberate second action, so a plain selection must not isolate.
    /// </summary>
    [Fact]
    public async Task SelectingANodeHighlightsItWithoutNarrowingTheView()
    {
        var module = StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeSelected(["actions/checkout"]));

        Assert.Contains("actions/checkout", page.Markup);
        // "only X" is the isolation banner. A highlight has not earned it.
        Assert.DoesNotContain("only actions/checkout", page.Markup);
        Assert.Empty(module.Invocations["fit"]);
    }

    /// <summary>
    /// Isolating narrows to the immediate neighbors and says so, because an unbounded walk from
    /// any node reaches nearly the whole graph and would look like no change at all.
    /// </summary>
    [Fact]
    public async Task IsolatingANodeNarrowsTheViewAndNamesWhatItNarrowedTo()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeIsolated("actions/checkout"));

        Assert.Contains("only actions/checkout", page.Markup);
    }

    /// <summary>
    /// Isolating the same node twice is the way back out. Without the toggle the only exit is the
    /// close button, and a second click would appear to do nothing.
    /// </summary>
    [Fact]
    public async Task IsolatingTheSameNodeTwiceReturnsToTheWholeView()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeIsolated("actions/checkout"));
        await page.InvokeAsync(() => page.Instance.OnNodeIsolated("actions/checkout"));

        Assert.DoesNotContain("only actions/checkout", page.Markup);
    }

    /// <summary>
    /// Clearing tells the module as well as the page. A clear that updates only C# leaves the
    /// canvas still drawing the old highlight, which is the state this pair exists to prevent.
    /// </summary>
    [Fact]
    public async Task ClearingASelectionReachesTheGraphModuleAndNotOnlyThePage()
    {
        var module = StubGraphModule();
        var page = Render<Explorer>();
        await page.InvokeAsync(() => page.Instance.OnNodeIsolated("actions/checkout"));

        await page.InvokeAsync(() => page.Instance.OnGraphCleared());

        Assert.DoesNotContain("only actions/checkout", page.Markup);
        var cleared = module.Invocations["setSelection"];
        Assert.Contains(cleared, i => i.Arguments[1] is string[] { Length: 0 });
    }

    /// <summary>
    /// Only isolation changed which nodes are on screen, so only isolation has earned a camera
    /// move. Recentering after merely dropping a highlight loses the reader's place for nothing.
    /// </summary>
    [Fact]
    public async Task DroppingAHighlightLeavesTheCameraWhereItWas()
    {
        var module = StubGraphModule();
        var page = Render<Explorer>();
        await page.InvokeAsync(() => page.Instance.OnNodeSelected(["actions/checkout"]));

        await page.InvokeAsync(() => page.Instance.OnGraphCleared());

        Assert.Empty(module.Invocations["fit"]);
    }

    /// <summary>And the counterpart: leaving an isolated view does recenter, because the set of
    /// nodes on screen has changed underneath the reader.</summary>
    [Fact]
    public async Task LeavingAnIsolatedViewRecenters()
    {
        var module = StubGraphModule();
        var page = Render<Explorer>();
        await page.InvokeAsync(() => page.Instance.OnNodeIsolated("actions/checkout"));

        await page.InvokeAsync(() => page.Instance.OnGraphCleared());

        Assert.NotEmpty(module.Invocations["fit"]);
    }

    /// <summary>
    /// Escape with nothing selected must do nothing at all. Without the guard every stray key
    /// press re-runs the filters and moves the camera on a view nobody asked to change.
    /// </summary>
    [Fact]
    public async Task ClearingWithNothingSelectedDoesNotTouchTheGraph()
    {
        var module = StubGraphModule();
        var page = Render<Explorer>();
        var before = module.Invocations["applyFilters"].Count;

        await page.InvokeAsync(() => page.Instance.OnGraphCleared());

        Assert.Empty(module.Invocations["fit"]);
        Assert.Equal(before, module.Invocations["applyFilters"].Count);
    }

    /// <summary>
    /// A shared link arrives already isolated, so the page has to reach the same state a click
    /// produces rather than merely filling in a label.
    /// </summary>
    [Fact]
    public void ALinkThatNamesASelectionArrivesIsolated()
    {
        StubGraphModule();
        // Supplied through navigation because the page reads it from the query string, which is
        // the same path a shared link takes.
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?selection=actions%2Fcheckout");

        var page = Render<Explorer>();

        Assert.Contains("only actions/checkout", page.Markup);
    }

    /// <summary>
    /// More than three selected nodes are counted rather than listed: the label sits in a fixed
    /// strip, and a joined list of forty keys pushes everything beside it off the row.
    /// </summary>
    [Fact]
    public async Task ManySelectedNodesAreCountedRatherThanListed()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeSelected(
            ["a/one", "b/two", "c/three", "d/four"]));

        Assert.Contains("4 nodes selected", page.Markup);
        Assert.DoesNotContain("c/three", page.Markup);
    }

    /// <summary>
    /// Remediation is where a reader finds the dependency they have to do something about, and
    /// its Explore link is the one hand-off into the narrow view. It named <c>focus</c>, which the
    /// page does not read, so the reader arrived at the whole estate with their node nowhere in
    /// particular — the narrow view unreachable from the one place that tries to reach it.
    /// </summary>
    [Fact]
    public void TheHandOffFromRemediationArrivesAtTheNodeItNamed()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?focus=actions%2Fcheckout");

        var page = Render<Explorer>();

        Assert.Contains("only actions/checkout", page.Markup);
    }

    /// <summary>
    /// `direction` is checked against the options and `view` was not, on adjacent lines. A link
    /// naming a view that does not exist left the toggle group with nothing selected, and the bad
    /// name then propagated into the address bar and out into every link shared from there.
    /// </summary>
    [Fact]
    public async Task AViewNobodyOffersIsIgnoredRatherThanApplied()
    {
        StubGraphModule();
        var nav = Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!;
        nav.NavigateTo("/explore?view=risk");

        var page = Render<Explorer>();
        await page.InvokeAsync(() => page.Instance.SyncAddress());

        Assert.Contains("view=neighborhood", nav.Uri);
        Assert.DoesNotContain("view=risk", nav.Uri);
    }

    /// <summary>
    /// A handed-over key can name nothing this graph holds — the scope may exclude it, or a kind
    /// filter may have dropped it. Drawing an empty canvas under a banner naming the node states
    /// the opposite of what happened: that the node is here and touches nothing.
    /// </summary>
    [Fact]
    public void AFocusThatNamesNothingInTheGraphSaysSoRatherThanDrawingNothing()
    {
        StubGraphModule(new Explorer.Counts(0, 0, 0, 2639, FocusFound: 0));
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?focus=octo%2Fnot-here");

        var page = Render<Explorer>();

        Assert.Contains("not in this view", page.Markup);
    }

    /// <summary>The same banner must stay quiet when the focus did resolve.</summary>
    [Fact]
    public void AFocusThatResolvedSaysNothingAboutBeingMissing()
    {
        StubGraphModule(new Explorer.Counts(7, 6, 0, 2639, FocusFound: 2));
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?focus=actions%2Fcheckout");

        var page = Render<Explorer>();

        Assert.Contains("only actions/checkout", page.Markup);
        Assert.DoesNotContain("not in this view", page.Markup);
    }

    private BunitJSModuleInterop StubGraphModule(Explorer.Counts? counts = null)
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(counts ?? new Explorer.Counts(0, 0, 0, 0));
        foreach (var call in new[]
                 {
                     "setPalette", "setAnimate", "setGroupBy", "setCollapsed", "setView",
                     "setGeneration", "setSelection", "resize", "rebuildNodes", "fit", "focus",
                 })
        {
            module.SetupVoid(call, _ => true).SetVoidResult();
        }
        return module;
    }
}
