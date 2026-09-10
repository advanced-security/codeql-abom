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
        Assert.Contains("nodes out of", page.Markup);
    }

    /// A page that draws nothing must not report nodes it does not have. The counts are the
    /// module's answer, so this is the assertion the harness exists to make possible.
    [Fact]
    public void TheExplorerReportsTheCountTheModuleGaveItAndNoOther()
    {
        StubGraphModule(new Explorer.Counts(Nodes: 7, Edges: 3, Hidden: 2, Total: 9));

        var page = Render<Explorer>();

        Assert.Contains("<b>7</b> nodes out of", page.Markup);
        Assert.Contains("9 total", page.Markup);
        Assert.Contains("2 nodes hidden by filters", page.Markup);
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

    /// A stub that answers only what the page happens to call today is a stub that breaks on the
    /// next call added. Naming the contract is what makes the harness reusable.
    private void StubGraphModule(Explorer.Counts? counts = null)
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
    }
}
