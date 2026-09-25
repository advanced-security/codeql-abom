using System.Linq;
using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Explorer draws only a scope somebody chose.
///
/// <para>It used to open on the whole corpus grouped by owner, which the interface audit measured
/// as 614 owner-groups gathered from 12,489 nodes — a saturated starburst in which no single edge
/// can be followed from one end to the other. Grouping by repository made it worse, at 3,356
/// groups over the same nodes, so the fix is a smaller scope rather than a better fold.</para>
///
/// <para>Then it opened on the advisory routes with findings, which draws <b>205</b> nodes on the
/// one crawled organization. That is a measurement of this corpus, not a property of the scope:
/// a large account produces a different number from the same predicate, and an arrival scope is a
/// promise about size that the corpus decides. So there is no arrival scope. Nothing is drawn
/// until a reader picks a view, builds one with the filter picker, or arrives from another page
/// with terms already in the address.</para>
/// </summary>
public sealed class ExplorerScopeTests : AbomTestContext
{
    /// <summary>
    /// Nothing is requested until there is something to request it for. Drawing a guessed scope
    /// spends a full graph load on a question nobody asked, and teaches a reader that whatever
    /// appeared is what ABOM thinks they should be looking at.
    /// </summary>
    [Fact]
    public void NothingIsDrawnUntilAScopeIsChosen()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-no-scope]")));
        Assert.Empty(JSInterop.Invocations["loadFull"]);
    }

    /// <summary>
    /// Arriving with terms in the address is the third route in, and the one every link from
    /// Inventory and Discovery uses. It must draw at once and ask nothing.
    /// </summary>
    [Fact]
    public void ArrivingWithTermsInTheAddressDrawsAtOnce()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?scope=exposure&flags=findings");

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.Contains("scope=exposure", LoadedUrl()));
        Assert.Empty(page.FindAll("[data-abom-no-scope]"));
    }

    /// <summary>
    /// Route (a): a scope somebody already named. The Explorer had no saved views at all — only
    /// Inventory offered them — so the one question this page exists for could be reached only by
    /// rebuilding it by hand each time.
    /// </summary>
    [Fact]
    public void TheEmptyExplorerOffersTheViewsSomebodyAlreadyNamed()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        Answer("api/views", """
            [{"name":"exposure-findings","title":"Advisory routes, with findings",
              "predicate":"flags=findings&scope=exposure","builtin":true,"refuses":{"graph":[]}}]
            """);
        StubGraphModule();

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
            Assert.NotNull(page.Find("[data-abom-views] [data-abom-view='exposure-findings']")));
    }

    /// Choosing one of them draws it, or offering them is a list that does nothing.
    [Fact]
    public void ChoosingAnOfferedViewDrawsIt()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        Answer("api/views", """
            [{"name":"exposure-findings","title":"Advisory routes, with findings",
              "predicate":"flags=findings&scope=exposure","builtin":true,"refuses":{"graph":[]}}]
            """);
        StubGraphModule();

        var page = Render<Explorer>();
        page.WaitForElement("[data-abom-views] [data-abom-view='exposure-findings']").Click();

        page.WaitForAssertion(() => Assert.Contains("scope=exposure", LoadedUrl()));
    }

    /// <summary>
    /// Route (b): define one here. The picker is the same component Inventory uses, and it has to
    /// be reachable before anything is drawn — a control that only appears once there is a graph
    /// cannot be how a reader gets one.
    /// </summary>
    [Fact]
    public void TheEmptyExplorerOffersThePickerToBuildOne()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-add-filter]")));
    }

    /// <summary>
    /// Having chosen nothing is not an error, and it is not the draw ceiling's refusal either. A
    /// reader has to be able to tell <em>choose something</em> from <em>you chose too much</em>,
    /// and both from <em>there is nothing here</em>.
    /// </summary>
    [Fact]
    public void ChoosingNothingYetReadsAsAChoiceRatherThanAnEmptyCorpusOrARefusal()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-no-scope]")));
        Assert.Empty(page.FindAll("[data-abom-draw-refused]"));
        Assert.Empty(page.FindAll("[data-abom-empty-corpus]"));
    }

    /// <summary>
    /// The controls that arrange a picture are absent until there is one. A view selector, a
    /// grouping, a collapse switch and a legend of node kinds over an empty canvas are controls
    /// for something that does not exist: touching any of them changes nothing, which teaches a
    /// reader that the controls do nothing rather than that nothing is drawn.
    /// </summary>
    [Fact]
    public void TheControlsThatArrangeAPictureAreAbsentUntilThereIsOne()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();
        page.WaitForElement("[data-abom-no-scope]");

        Assert.Empty(page.FindAll(".abom-view-select"));
        Assert.Empty(page.FindAll("[data-abom-layout-state]"));
        Assert.Empty(page.FindAll("[data-abom-legend]"));
        // "Showing 0 nodes out of 0 total" is a measurement of nothing written as a measurement.
        Assert.NotNull(page.Find("[data-abom-nothing-counted]"));
        // The filter picker stays: it is how a reader gets a picture, not how they arrange one.
        Assert.NotNull(page.Find("[data-abom-add-filter]"));
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
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?scope=exposure&flags=findings");

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
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?scope=exposure&flags=findings");

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
