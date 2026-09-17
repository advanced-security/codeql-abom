using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A named view is a predicate, not a page and no longer a tab. The tab strip is the unit switch —
/// a node kind whose columns are its own — so the views moved into the scope bar, where choosing
/// one sets the scope and the reader can then narrow further.
///
/// <para>Chips rather than a dropdown, for the reason the exports are not in one either: MudMenu
/// and MudSelect render their items into a popover only once opened, so what is on offer is
/// neither discoverable to a reader nor assertable by a test.</para>
/// </summary>
public class InventoryViewMenuTests : AbomTestContext
{
    private const string Views = """
        [{"name":"owned-findings","title":"What you own, with findings",
          "predicate":"flags=findings&scope=owned","builtin":true},
         {"name":"unpinned","title":"Floating references","predicate":"ref=tag","builtin":true},
         {"name":"my-scope","predicate":"kinds=action","builtin":false,"createdBy":"octo"}]
        """;

    private const string Page = """
        {"items":[{"nodeKey":"actions/checkout","kind":"action","origin":"external",
          "dependentRepos":50,"directDependents":60,"distinctRefs":3,
          "findings":2,"advisories":0,"analyzed":true,"refs":["v4"]}],"total":7}
        """;

    public InventoryViewMenuTests()
    {
        Answer("api/views", Views);
        Answer("api/actions", Page);
    }

    private string LastActionsRequest => Sent.Last(r => r.Url.Contains("api/actions")).Url;

    private IRenderedComponent<Pages.Inventory> RenderInventory()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/views")));
        return page;
    }

    [Fact]
    public void EveryNamedViewTheServerReportsIsOnOfferWithoutOpeningAnything()
    {
        var page = RenderInventory();

        page.WaitForAssertion(() =>
        {
            Assert.NotNull(page.Find("[data-abom-view='owned-findings']"));
            Assert.NotNull(page.Find("[data-abom-view='unpinned']"));
        });
    }

    /// <summary>
    /// A saved scope sits beside the built-ins, or the durable artifact an operator made is less
    /// reachable than the ones that shipped.
    /// </summary>
    [Fact]
    public void ASavedViewAppearsBesideTheBuiltIns()
    {
        var page = RenderInventory();

        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-view='my-scope']")));
    }

    /// <summary>
    /// Choosing a view loads its predicate into the scope rather than sending its name, so the
    /// reader can then narrow further. Sending the name would be refused the moment they did.
    /// </summary>
    [Fact]
    public void ChoosingAViewLoadsItsPredicateIntoTheScope()
    {
        var page = RenderInventory();
        page.Find("[data-abom-view='unpinned']").Click();

        page.WaitForAssertion(() => Assert.Contains("ref=tag", LastActionsRequest));
        Assert.DoesNotContain("view=unpinned", LastActionsRequest);
    }

    /// <summary>
    /// The view in force is marked, or a reader cannot narrow away from a scope they cannot see
    /// they are in.
    /// </summary>
    [Fact]
    public void TheViewInForceIsMarked()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?ref=tag");

        var page = RenderInventory();

        page.WaitForAssertion(() => Assert.Contains(
            "abom-view-selected", page.Find("[data-abom-view='unpinned']").ClassName ?? ""));
    }

    /// <summary>
    /// And narrowing past a view leaves it behind rather than going on claiming it.
    /// </summary>
    [Fact]
    public void NarrowingPastAViewNoLongerClaimsIt()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?q=checkout&ref=tag");

        var page = RenderInventory();

        page.WaitForAssertion(() => Assert.DoesNotContain(
            "abom-view-selected", page.Find("[data-abom-view='unpinned']").ClassName ?? ""));
    }
}
