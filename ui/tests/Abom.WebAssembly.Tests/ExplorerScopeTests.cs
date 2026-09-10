using System.Linq;
using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Explorer opens on the scope the operator declared.
///
/// <para>It used to open on the whole corpus grouped by owner, which the interface audit measured
/// as 614 owner-groups gathered from 12,489 nodes — a saturated starburst in which no single edge
/// can be followed from one end to the other. Grouping by repository made it worse, at 3,356
/// groups over the same nodes, so the fix is a smaller scope rather than a better fold.</para>
/// </summary>
public sealed class ExplorerScopeTests : AbomTestContext
{
    /// The declared closure is the arrival scope. Opening on the whole corpus teaches a new
    /// operator that ABOM works before ownership is claimed, which is the conflation this ends.
    [Fact]
    public void TheExplorerOpensOnWhatTheOperatorOwns()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        Render<Explorer>();

        Assert.Contains("scope=owned", LoadedUrl());
    }

    /// The scope has two halves. Owned is the right population and still an unreadable starburst;
    /// operator testing found the readable configuration is owned **and has findings**, which is
    /// **177 groups by repository** against **1,564** for the same closure unfiltered. The filter
    /// does the work, not the fold.
    ///
    /// <para>It is not an arbitrary narrowing either: it is the question the page exists to answer.
    /// Not "what do I depend on" but "what do I depend on that something is wrong with".</para>
    [Fact]
    public void TheArrivalScopeIsWhatYouOwnThatHasFindings()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        Render<Explorer>();

        Assert.Contains("scope=owned", LoadedUrl());
        Assert.True(FilterFlag("hasFindings"),
            "the closure alone is 4,805 nodes and unreadable; findings is the half that makes it a picture");
    }

    /// Uncollapsed, with the parent structure present. Grouping supplies the owner and repository
    /// context every in-scope node is drawn inside; collapsing gathers those nodes away into a
    /// group the reader has to click to see into, which is the opposite of the goal once the
    /// population is small enough to draw.
    [Fact]
    public void TheParentStructureIsPresentAndNotCollapsed()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        Render<Explorer>();

        Assert.Equal("owner", JSInterop.Invocations["setGroupBy"].First().Arguments[1]);
        Assert.False((bool)JSInterop.Invocations["setCollapsed"].First().Arguments[1]!,
            "collapsing hides the nodes this scope exists to show");
    }

    /// The whole corpus is not on offer. Measured in a browser on 2026-09-11 it draws at **1.5
    /// FPS** — and removing every edge in it only reaches 4.8, so no amount of edge reduction
    /// rescues it. It is unscalable by construction: it grows with what the crawl walked through
    /// rather than with anything the operator owns.
    [Fact]
    public void TheWholeCorpusIsNotOfferedAsAView()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        Assert.DoesNotContain("Everything discovered", page.Markup);
    }

    /// The structural half of the same rule, and the one that cannot be satisfied by renaming a
    /// label: an empty scope is what asks the API for everything, so no reachable choice may
    /// carry one.
    [Fact]
    public void EveryScopeAReaderCanChooseIsBounded()
    {
        Assert.NotEmpty(Explorer.ScopeKeys);
        Assert.DoesNotContain("", Explorer.ScopeKeys);
    }

    /// The scope reaches the request, not merely the toggle. A bounded choice that still loaded
    /// the unscoped URL would be the same hairball under a different name.
    [Fact]
    public void TheGraphIsNeverRequestedWithoutAScope()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        Render<Explorer>();

        Assert.All(JSInterop.Invocations["loadFull"],
            call => Assert.Contains("scope=", (string)call.Arguments[1]!));
    }

    private string LoadedUrl() =>
        (string)JSInterop.Invocations["loadFull"].First().Arguments[1]!;

    /// The filter spec is an anonymous object handed to graph.js, so reading it back is how a test
    /// asserts the contract the browser module actually sees.
    private bool FilterFlag(string field)
    {
        var spec = JSInterop.Invocations["applyFilters"].First().Arguments[1]!;
        return (bool)spec.GetType().GetProperty(field)!.GetValue(spec)!;
    }

    private void StubGraphModule()
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(new Explorer.Counts(0, 0, 0, 0));
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
