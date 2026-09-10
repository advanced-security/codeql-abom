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

    private void StubGraphModule()
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
    }
}
