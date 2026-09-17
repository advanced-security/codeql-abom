using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Explorer's empty state is a stage, not an apology.
///
/// <para>Before anything is declared it says so and points at Settings. After a declaration and
/// before there is anything to draw, it says what it is waiting for. After that it is a picture.
/// Shipped, all three looked identical: an empty canvas with "Showing 0 nodes out of 0 total",
/// which reads as a broken page rather than as a first run.</para>
/// </summary>
public sealed class ExplorerFrameTests : AbomTestContext
{
    /// The one frame that needs no renderer at all: the declaration is the page's own to read.
    [Fact]
    public void NothingDeclaredPointsAtSettingsRatherThanDrawingAnEmptyCanvas()
    {
        Answer("api/owned-scope", "[]");
        StubGraphModule();

        var page = Render<Explorer>();

        var frame = page.Find("[data-abom-explorer-frame]");
        Assert.Contains("Settings", frame.TextContent);
    }

    /// Declared but nothing drawn yet is a different sentence from nothing declared: the operator
    /// has already acted, and telling them to go and declare something would be wrong.
    [Fact]
    public void ADeclarationWithNothingDrawnYetSaysWhatItIsWaitingFor()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        var frame = page.Find("[data-abom-explorer-frame]");
        Assert.DoesNotContain("Settings", frame.TextContent);
        Assert.Contains("octo-org", frame.TextContent);
    }

    /// Once there is something to draw, the frame gets out of the way.
    [Fact]
    public void AGraphWithNodesDrawsThePictureAndNoFrame()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(new Explorer.Counts(Nodes: 12, Edges: 4, Hidden: 0, Total: 12));

        var page = Render<Explorer>();

        Assert.Empty(page.FindAll("[data-abom-explorer-frame]"));
    }

    /// A page that has not been answered yet must not announce that there is nothing. The frame
    /// is a claim about the corpus, and claiming it before the read returns is the same class of
    /// error as an absent measurement drawn as a measurement.
    [Fact]
    public void TheFrameWaitsForTheDeclarationRatherThanAssumingItIsEmpty()
    {
        Answer("api/owned-scope", "[]");
        var release = Withhold("api/owned-scope");
        StubGraphModule();

        var page = Render<Explorer>();

        Assert.Empty(page.FindAll("[data-abom-explorer-frame]"));
        release();
    }

    private void StubGraphModule(Explorer.Counts? counts = null)
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
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
