using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The grid used to fetch 500 rows and page inside them. On the real corpus that was 500 of 1,585,
/// with nothing on screen saying so, and sorting by findings named a row holding 39 while the true
/// answer held 50 — because the row holding 50 was never fetched.
/// </summary>
public class InventoryPageTests : AbomTestContext
{
    private const string Page = """
        {"items":[
          {"nodeKey":"actions/checkout","kind":"action","origin":"external",
           "dependentRepos":50,"dependentSteps":60,"distinctRefs":3,
           "findings":2,"advisories":0,"analyzed":true,"refs":["v4"]},
          {"nodeKey":"actions/cache","kind":"action","origin":"external",
           "dependentRepos":20,"dependentSteps":22,"distinctRefs":1,
           "findings":1,"advisories":1,"analyzed":false,"refs":["v3"]}],
         "total":1585}
        """;

    private string LastActionsRequest =>
        Sent.Last(r => r.Url.Contains("api/actions")).Url;

    /// <summary>
    /// A page that does not say what it left out reads as the whole estate, which is the coverage
    /// lie the interface is not allowed to tell.
    /// </summary>
    [Fact]
    public void TheGridSaysHowManyRowsItIsNotShowing()
    {
        Answer("api/actions", Page);

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains("of 1,585", page.Markup));
    }

    /// <summary>
    /// Sorting has to be asked of the server. Reordering the rows already fetched answers "the most
    /// findings among the widest-reaching page", which is not what the column header says.
    /// </summary>
    [Fact]
    public void SortingIsAskedOfTheServerRatherThanAppliedToThePage()
    {
        Answer("api/actions", Page);

        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        page.FindAll(".sortable-column-header")
            .First(h => h.TextContent.Contains("Findings")).Click();

        page.WaitForAssertion(() => Assert.Contains("sort=findings", LastActionsRequest));
    }

    /// <summary>
    /// The listing reads the shared predicate, so a scope built anywhere narrows it. Carrying the
    /// term and not sending it would show the operator a wider answer than the one they asked for.
    /// </summary>
    [Fact]
    public void AScopeInTheAddressReachesTheRequest()
    {
        Answer("api/actions", Page);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?properties=business_id%3A3");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
            Assert.Contains("properties=business_id%3A3", LastActionsRequest));
    }

    /// <summary>
    /// A page is a cursor, not a slice of what was already downloaded.
    /// </summary>
    [Fact]
    public void TheFirstRequestAsksForOnePageRatherThanTheWholeMatch()
    {
        Answer("api/actions", Page);

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            Assert.Contains("offset=0", LastActionsRequest);
            Assert.DoesNotContain("top=500", LastActionsRequest);
        });
    }

    /// <summary>
    /// The Actions code scanning term travels from the address to the repositories listing. The
    /// browser could not read it, so the address was refused whole and every repository listed.
    /// </summary>
    [Fact]
    public void ACodeScanningClassInTheAddressReachesTheRepositoriesRequest()
    {
        Answer("api/repositories", """{"items":[],"total":0}""");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?unit=repositories&codeScanning=enable");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains("codeScanning=enable",
            Sent.Last(r => r.Url.Contains("api/repositories")).Url));
    }

    /// <summary>
    /// An address term this page cannot read is refused by name, never dropped: the rows under a
    /// silent refusal answer a wider question than the address asked, and nothing says so.
    /// </summary>
    [Fact]
    public void ATermTheAddressCarriesAndThisPageCannotReadIsRefusedByName()
    {
        Answer("api/actions", Page);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?kindz=action");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
            Assert.Contains("kindz", page.Find("[data-abom-scope-refusal]").TextContent));
    }
}
