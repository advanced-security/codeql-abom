using System.Linq;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

// A filter that does not survive a reload is a setting on one screen, not a scope.
//
// The Explorer, Discovery and the node page already carried their state in the address. The four
// surfaces named here lost everything on a reload, so a filtered view could not be shared,
// bookmarked, or returned to after following a link out of it. Three of those four are units of
// Inventory now, and each is still asked for by the address that names it.
public sealed class FilterPersistenceTests : AbomTestContext
{
    private const string Document = """
        {"roots":["octo-org/app"],"generatedAt":"2026-09-14T00:00:00Z",
         "components":[
           {"key":"actions/checkout","kind":"action","repository":"actions/checkout",
            "direct":true,"depth":1,"analyzed":true,"refsInUse":1,"refsAnalyzed":1,
            "references":[{"raw":"actions/checkout@v4","value":"v4","analyzed":true}],
            "findings":[],"advisories":[]},
           {"key":"actions/setup-node","kind":"action","repository":"actions/setup-node",
            "direct":true,"depth":1,"analyzed":true,"refsInUse":1,"refsAnalyzed":1,
            "references":[{"raw":"actions/setup-node@v4","value":"v4","analyzed":true}],
            "findings":[],"advisories":[]}],
         "relationships":[],
         "coverage":{"componentsInUse":2,"componentsAnalyzed":2,"referencesInUse":2,
                     "referencesAnalyzed":2,"findings":0,"findingsUntraced":0,
                     "findingsWithoutHash":0,"findingsWithoutDigest":0}}
        """;

    private const string NoProperties = """
        {"configured":"","keys":[],"breakdown":{"name":"","repositories":0,"withValue":0,
         "withoutValue":0,"values":[],"hasDefault":false,"defaultValue":""}}
        """;

    private const string Views = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]
        """;

    private const string Page = """{"items":[],"total":0}""";

    private void Go(string url) =>
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo(url);

    [Fact]
    public void TheBillOfMaterialsOpensFilteredWhenTheAddressSaysSo()
    {
        Answer("api/bom", Document);
        Answer("api/properties", NoProperties);
        Go("/bom?q=setup-node");

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("actions/setup-node", cut.Markup));
        Assert.DoesNotContain("actions/checkout", cut.Markup);
    }

    [Fact]
    public void VersionPinningAsksTheServerForWhatTheAddressAskedFor()
    {
        Answer("api/views", Views);
        Answer("api/actions", Page);
        Go("/inventory?ref=tag&q=checkout");

        var cut = Render<Pages.Inventory>();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        var asked = Sent.First(r => r.Url.Contains("api/actions")).Url;
        Assert.Contains("q=checkout", asked);
        Assert.Contains("ref=tag", asked);
    }

    [Fact]
    public void RepositoriesAsksTheServerForWhatTheAddressAskedFor()
    {
        Answer("api/views", Views);
        Answer("api/repositories", """{"items":[],"total":0}""");
        Go("/inventory?unit=repositories&q=codeql");

        var cut = Render<Pages.Inventory>();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/repositories")));
        Assert.Contains("q=codeql", Sent.First(r => r.Url.Contains("api/repositories")).Url);
    }

    [Fact]
    public void ActionsAndWorkflowsAsksTheServerForWhatTheAddressAskedFor()
    {
        Answer("api/views", Views);
        Answer("api/actions", Page);
        Go("/inventory?q=checkout");

        var cut = Render<Pages.Inventory>();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        Assert.Contains("q=checkout", Sent.First(r => r.Url.Contains("api/actions")).Url);
    }

    // The box shows the filter that is applied, not the name of the variable holding it.
    //
    // `Value="_search"` on a component passes the literal string `_search`, because Razor only
    // evaluates an expression behind `@`. Every page using the shared search box did this, so the
    // grid filtered correctly and the control above it read `_search` — the one place a reader
    // looks to find out what they are filtered by.
    [Fact]
    public void TheSearchBoxShowsTheTermItIsFilteringBy()
    {
        Answer("api/views", Views);
        Answer("api/actions", Page);
        Go("/inventory?q=codeql");

        var cut = Render<Pages.Inventory>();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        Assert.Equal("codeql", cut.Find(".abom-search input").GetAttribute("value"));
    }

    // Writing the filter back is the other half: a scope nobody can copy out of the address bar
    // cannot be shared, however faithfully it was read in.
    [Fact]
    public void ChangingAFilterWritesItToTheAddress()
    {
        Answer("api/views", Views);
        Answer("api/actions", Page);
        Go("/inventory");

        var cut = Render<Pages.Inventory>();
        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        cut.Find(".abom-search input").Input("codeql");

        cut.WaitForAssertion(() => Assert.Contains(
            "q=codeql",
            Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri));
    }
}
