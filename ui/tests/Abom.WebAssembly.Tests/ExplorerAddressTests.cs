using Abom.WebAssembly.Pages;
using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Explorer reads its predicate from the address and, until now, wrote back only presentation.
/// Change a rule filter, navigate away, come back: it was gone. These pin the round-trip closed.
/// </summary>
public class ExplorerAddressTests : AbomTestContext
{
    public ExplorerAddressTests()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        Answer("api/summary", "{}");
        Answer("api/properties", """
            {"configured":"","keys":[],
             "breakdown":{"name":"","repositories":0,"withValue":0,"withoutValue":0,
              "values":[],"hasDefault":false,"defaultValue":""}}
            """);
    }

    private string CurrentQuery() =>
        new Uri(Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri).Query;

    /// <summary>
    /// The predicate an operator arrived with has to still be in the address after the page
    /// re-syncs it, or sharing the link hands somebody a different question.
    /// </summary>
    [Fact]
    public void APredicateArrivedWithSurvivesInTheAddress()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?rules=abom%2Funpinned&kinds=action");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        var query = CurrentQuery();
        Assert.Contains("rules=abom%2Funpinned", query);
        Assert.Contains("kinds=action", query);
    }

    /// <summary>
    /// Presentation still travels, because a shared link that lost the selection would arrive
    /// showing the whole corpus.
    /// </summary>
    [Fact]
    public void PresentationStillTravelsAlongsideThePredicate()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?selection=actions%2Fcheckout&kinds=action");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        var query = CurrentQuery();
        Assert.Contains("selection=actions%2Fcheckout", query);
        Assert.Contains("kinds=action", query);
    }

    /// <summary>
    /// An empty predicate writes nothing, so the default view keeps a clean address rather than
    /// one carrying every default spelled out.
    /// </summary>
    [Fact]
    public void TheDefaultViewDoesNotSpellOutItsDefaults()
    {
        StubGraphModule();

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        Assert.DoesNotContain("minReach=", CurrentQuery());
        Assert.DoesNotContain("origins=", CurrentQuery());
    }

    /// <summary>
    /// What the page puts in the address has to be the same text the API and the gate read, or the
    /// three consumers disagree silently.
    /// </summary>
    [Fact]
    public void TheAddressCarriesTheCanonicalPredicateText()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?kinds=workflow,action");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        // Sorted and comma-joined, exactly as view.Spec.String writes it in Go.
        Assert.Contains("kinds=action%2Cworkflow", CurrentQuery());
    }

    /// <summary>
    /// Three terms the page wrote through the shared serializer and read through its own
    /// parameter list, under a different name or under none at all. Each was silently reset to
    /// its default on arrival, so a shared link showed the sender's question to nobody.
    /// </summary>
    [Theory]
    [InlineData("ref=tag", "ref=tag")]
    [InlineData("scope=exposure", "scope=exposure")]
    // Not the first of the three defaults, which the unnarrowed list would match as a prefix.
    [InlineData("edges=sameRepo", "edges=sameRepo")]
    public void ATermArrivedWithIsStillTheTermWrittenBack(string arrived, string expected)
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?" + arrived);

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        Assert.Contains(expected, CurrentQuery());
    }

    /// <summary>
    /// A scope built elsewhere — a saved view, an Inventory tab, a hand-written link — names terms
    /// this page has no control for. Dropping them on the way through would silently widen
    /// somebody else's question.
    /// </summary>
    [Fact]
    public void ATermTheExplorerHasNoControlForStillTravels()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?properties=business_id%3A3&severity=high");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        Assert.Contains("properties=business_id%3A3", CurrentQuery());
        Assert.Contains("severity=high", CurrentQuery());
    }

    /// <summary>
    /// The graph refuses a source rather than drawing it, and the page has no control for one.
    /// Carried, the refusal says why nothing is drawn; dropped, the reader is shown every finding's
    /// graph under a link that asked for one source's.
    /// </summary>
    [Fact]
    public void ASourceTheGraphCannotDrawStillTravels()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?source=custom");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        Assert.Contains("source=custom", CurrentQuery());
    }

    /// <summary>
    /// Almost every link into this page is partial — Discovery sends a node and a direction and
    /// nothing else — so a list the address does not name asks nothing. For kinds that is every
    /// kind the payload holds rather than none, which would hand the reader a blank canvas; for
    /// flags it is no flag, as it is on every other consumer. The arrival kinds reach the browser
    /// rather than the address, where they would narrow every other page by them.
    /// </summary>
    [Fact]
    public void APartialHandOffKeepsTheArrivalDefaults()
    {
        var module = StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?selection=actions%2Fcheckout&direction=in&from=discovery");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        var query = CurrentQuery();
        var filter = module.Invocations["applyFilters"].Last().Arguments[1]!;
        var kinds = (string[])filter.GetType().GetProperty("kinds")!.GetValue(filter)!;
        Assert.Equal(7, kinds.Length);
        Assert.DoesNotContain("flags=", query);
        // A node still has to be drawn inside something, and the declared closure is the one that
        // is not a guess about size. What you own alone would not hold the dependency handed off.
        Assert.Contains("scope=supply-chain", query);
    }

    /// <summary>
    /// A relationship kind is a term this page has no control for, and it travels like the rest.
    /// Dropped, a shared link naming one widened to every relationship.
    /// </summary>
    [Fact]
    public void ARelationshipKindTravelsThroughTheExplorer()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?edgeKinds=job-needs-job&scope=owned");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        Assert.Contains("edgeKinds=job-needs-job", CurrentQuery());
    }

    /// <summary>
    /// The breadcrumb back to where the reader came from used to survive only until they touched a
    /// filter, because re-syncing the address wrote the predicate and the view and nothing else.
    /// </summary>
    [Fact]
    public void TheWayBackSurvivesASync()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?selection=a%2Fb&direction=in&from=discovery&status=queued");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        var query = CurrentQuery();
        Assert.Contains("from=discovery", query);
        Assert.Contains("status=queued", query);
        Assert.Contains("direction=in", query);
    }

    /// <summary>
    /// Only a selection and a view change used to reach the address, so narrowing by anything
    /// else and then sharing the link handed over the unnarrowed graph.
    /// </summary>
    ///
    /// The search box debounces, so the address is reached a beat after the keystroke. Driven
    /// through <c>oninput</c> rather than <c>onchange</c> because that is the event MudBlazor
    /// listens to once a debounce interval is set.
    [Fact]
    public void ChangingAFilterReachesTheAddress()
    {
        StubGraphModule();

        var popover = Render<MudBlazor.MudPopoverProvider>();
        var page = Render<Explorer>();

        // A term the Explorer does not already answer: scope carries a default, so it is
        // correctly not offered a second time.
        page.WaitForElement("[data-abom-add-filter]").Click();
        popover.WaitForElement("[data-abom-term='q']").Click();
        popover.WaitForElement("[data-abom-value-entry] input").Input("checkout");
        popover.WaitForElement("[data-abom-value-apply]").Click();

        page.WaitForAssertion(() => Assert.Contains("q=checkout", CurrentQuery()));
    }

    /// <summary>
    /// An address naming a filter that does not exist is a link somebody has to fix, and the page
    /// that renders its default scope without comment tells them nothing is wrong.
    /// </summary>
    [Fact]
    public void AnAddressNamingAFilterNobodyHasSaysSo()
    {
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?kindz=action");

        var page = Render<Explorer>();

        Assert.Contains("kindz", page.Markup);
    }

    /// <summary>
    /// A property term cannot be applied in the browser: a node carries one grouping value, for the
    /// configured key only. A control that reached the address and not the request would narrow
    /// nothing while saying it had.
    /// </summary>
    [Fact]
    public void APropertyScopeIsSentToTheGraphRatherThanAppliedHere()
    {
        var module = StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/explore?properties=business_id%3A3");

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.Contains(
            module.Invocations["loadFull"],
            call => call.Arguments is [_, string url, ..]
                    && url.Contains("properties=business_id%3A3", StringComparison.Ordinal)));
    }

    private BunitJSModuleInterop StubGraphModule()
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(new Explorer.Counts(0, 0, 0, 0));
        foreach (var name in new[]
                 {
                     "setPalette", "setAnimate", "setGroupBy", "setCollapsed", "setView",
                     "setGeneration", "setSelection", "resize", "rebuildNodes",
                 })
        {
            module.SetupVoid(name, _ => true).SetVoidResult();
        }
        return module;
    }
}
