using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Whether a minimal module stub lets the Explorer render at all.
///
/// <para><c>ui/testing.md</c> poses this as an open, cheap question: the page could not be
/// rendered in bUnit because <c>OnAfterRenderAsync</c> imports <c>./js/graph.js</c> and every
/// test died on the null module. Five tests for the empty and waiting frames were written and
/// removed on that basis. If the stub is enough, the harness and the selection-state phase are
/// separable and only the first blocks <c>ui/first-run.md</c>.</para>
/// </summary>
public sealed class ExplorerHarnessTests : AbomTestContext
{
    public ExplorerHarnessTests()
    {
        Answer("api/graph/generation", "{\"generation\":0}");
        Answer("api/summary", "{}");
        ArriveWithAScope();
    }

    /// The claim under test: the page reaches the end of its first render with the module stubbed.
    [Fact]
    public void TheExplorerRendersOnceTheGraphModuleIsStubbed()
    {
        StubGraphModule();

        var page = Render<Explorer>();

        Assert.Contains("abom-graph", page.Markup);
    }

    /// The frame that was blocked. An empty graph has to read as empty rather than as still
    /// arriving, and the count comes back from the module — which is why it could not be asserted
    /// before the stub existed.
    [Fact]
    public void AnEmptyGraphSaysSoRatherThanClaimingToStillBeLoading()
    {
        StubGraphModule();

        var page = Render<Explorer>();

        Assert.DoesNotContain("Loading the whole graph", page.Markup);
        Assert.Contains("nodes</b> out of", page.Markup);
    }

    /// A page that draws nothing must not report nodes it does not have. The counts are the
    /// module's answer, so this is the assertion the harness exists to make possible.
    [Fact]
    public void TheExplorerReportsTheCountTheModuleGaveItAndNoOther()
    {
        StubGraphModule(new Explorer.Counts(Nodes: 7, Edges: 3, Hidden: 2, Total: 9));

        var page = Render<Explorer>();

        Assert.Contains("<b>7 nodes</b> out of", page.Markup);
        Assert.Contains("9 total", page.Markup);
        Assert.Contains("2 nodes hidden by filters", page.Markup);
    }

    /// A count and the noun beside it have to agree, and one is the only value where they can
    /// disagree — so it is the only value that ever catches this.
    [Fact]
    public void ACountOfOneTakesASingularNoun()
    {
        StubGraphModule(new Explorer.Counts(Nodes: 1, Edges: 0, Hidden: 1, Total: 2));

        var page = Render<Explorer>();

        Assert.Contains("<b>1 node</b> out of", page.Markup);
        Assert.Contains("1 node hidden by filters", page.Markup);
    }

    /// Filters removing nothing must not produce a "0 hidden" clause: absent and zero are
    /// different claims, and the interface follows the same rule the store does.
    [Fact]
    public void NothingHiddenDrawsNoHiddenClause()
    {
        StubGraphModule(new Explorer.Counts(Nodes: 7, Edges: 3, Hidden: 0, Total: 7));

        var page = Render<Explorer>();

        Assert.DoesNotContain("hidden by filters", page.Markup);
    }

    /// <summary>
    /// The view opens flat. A third axis projects unrelated groups through each other, and on the
    /// reference corpus at one moment that was the difference between drawing 16 of 21 boundaries
    /// honestly and drawing 10 of 23 — the rest being refused for enclosing somebody else's node.
    /// </summary>
    [Fact]
    public void TheExplorerOpensFlat()
    {
        var module = StubGraphModule();

        Render<Explorer>();

        var create = Assert.Single(module.Invocations["create"]);
        Assert.False((bool)create.Arguments[1]!, "the Explorer opened in 3D");
    }

    /// <summary>
    /// An edge is drawn only when both of its ends survive the filter, so a scope that keeps 15%
    /// of the nodes keeps well under 1% of the relationships. Measured on the reference corpus:
    /// 395 of 2,639 nodes left 39 of 7,552 edges, and the view read as a bug — a dependency graph
    /// with almost no dependencies in it, and nothing on screen admitting to it.
    /// </summary>
    [Fact]
    public void AnEdgeNeedsBothEndsAndTheViewSaysSo()
    {
        StubGraphModule(new Explorer.Counts(
            Nodes: 395, Edges: 39, Hidden: 2244, Total: 2639, EdgeTotal: 7552));

        var page = Render<Explorer>();

        Assert.Contains("39", page.Markup);
        Assert.Contains("7,552", page.Markup);
        Assert.Contains("both ends", page.Markup);
    }

    /// <summary>
    /// A node that failed a scope question is greyed rather than removed, because cutting it took
    /// the relationships with it. It is on screen without having matched, so it is counted apart.
    /// </summary>
    [Fact]
    public void NodesDrawnOnlyAsContextAreCountedApart()
    {
        StubGraphModule(new Explorer.Counts(
            Nodes: 2639, Edges: 7552, Hidden: 0, Total: 2639, EdgeTotal: 7552, Context: 2244));

        var page = Render<Explorer>();

        Assert.Contains("2,244", page.Markup);
        Assert.Contains("greyed out", page.Markup);
        Assert.Contains("not because they matched", page.Markup);
    }

    /// Nothing elided, nothing to disclose: the same rule the hidden-node clause follows.
    [Fact]
    public void EveryRelationshipDrawnDrawsNoClause()
    {
        StubGraphModule(new Explorer.Counts(
            Nodes: 7, Edges: 3, Hidden: 0, Total: 7, EdgeTotal: 3));

        var page = Render<Explorer>();

        Assert.DoesNotContain("both ends", page.Markup);
    }

    /// A stub that answers only what the page happens to call today is a stub that breaks on the
    /// next call added. Naming the contract is what makes the harness reusable.
    private BunitJSModuleInterop StubGraphModule(Explorer.Counts? counts = null)
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        // Every typed call needs a result. Loose mode answers `default`, which for a record is
        // null, and the page dereferences what the module hands back.
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(counts ?? new Explorer.Counts(0, 0, 0, 0));
        module.SetupVoid("setPalette", _ => true).SetVoidResult();
        module.SetupVoid("setAnimate", _ => true).SetVoidResult();
        module.SetupVoid("setGroupBy", _ => true).SetVoidResult();
        module.SetupVoid("setCollapsed", _ => true).SetVoidResult();
        module.SetupVoid("setView", _ => true).SetVoidResult();
        module.SetupVoid("setGeneration", _ => true).SetVoidResult();
        module.SetupVoid("setSelection", _ => true).SetVoidResult();
        module.SetupVoid("resize", _ => true).SetVoidResult();
        module.SetupVoid("rebuildNodes", _ => true).SetVoidResult();
        return module;
    }
}
