using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Every term a scope carries is applied on this page, handed to the browser filter, or refused
/// on screen. Measured on the crawled organization before these existed: a scope carrying
/// <c>boundary</c>, <c>edgeKinds</c>, <c>notRules</c>, <c>provider</c> or <c>severity</c> opened
/// enabled and drew exactly what <c>scope=owned</c> alone draws, and one carrying <c>source</c> or
/// <c>repositories</c> drew it too with nothing saying so.
/// </summary>
public sealed class ExplorerTermsTests : AbomTestContext
{
    private const string Catalog = """
        [
          {"name":"scope","label":"Scope","help":"h","arity":"scalar","values":["owned","exposure"],
           "surfaces":["graph","inventory"]},
          {"name":"ref","label":"Reference style","help":"h","arity":"scalar",
           "values":["any","sha","tag"],"surfaces":["graph","inventory"]},
          {"name":"flags","label":"Has","help":"h","arity":"list",
           "values":["findings","advisory","unanalyzed"],"surfaces":["graph","inventory"]},
          {"name":"source","label":"Source","help":"h","arity":"list",
           "values":["upstream","abom","custom","mixed"],"surfaces":["set","inventory"]},
          {"name":"repositories","label":"Finding repository","help":"h","arity":"list",
           "surfaces":["set","inventory"]},
          {"name":"severity","label":"Severity","help":"h","arity":"scalar","surfaces":["graph","set"]}
        ]
        """;

    public ExplorerTermsTests()
    {
        Answer("api/filters/counts", "{}");
        Answer("api/filters", Catalog);
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        Answer("api/graph/version", "{\"generation\":0}");
        Answer("api/summary", "{}");
        Answer("api/views", "[]");
    }

    private void Arrive(string query) =>
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/explore?" + query);

    private string CurrentQuery() =>
        new Uri(Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri).Query;

    private static object LastFilter(BunitJSModuleInterop module) =>
        module.Invocations["applyFilters"].Last().Arguments[1]!;

    private static T Field<T>(object filter, string name) =>
        (T)filter.GetType().GetProperty(name)!.GetValue(filter)!;

    [Fact]
    public void TheRelationshipTermsTheGraphAppliesAreSentToIt()
    {
        var module = StubGraphModule();
        Arrive("scope=owned&boundary=cross-owner&edgeKinds=job-needs-job");

        Render<Explorer>();

        var url = (string)module.Invocations["loadFull"].Last().Arguments[1]!;
        Assert.Contains("boundary=cross-owner", url);
        Assert.Contains("edgeKinds=job-needs-job", url);
    }

    [Fact]
    public void TheFindingTermsReachTheBrowserFilter()
    {
        var module = StubGraphModule();
        Arrive("scope=owned&notRules=actions%2Funpinned-tag&provider=CodeQL&severity=error");

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
        {
            var filter = LastFilter(module);
            Assert.Equal(["actions/unpinned-tag"], Field<string[]>(filter, "notRules"));
            Assert.Equal("CodeQL", Field<string>(filter, "provider"));
            Assert.Equal("error", Field<string>(filter, "severity"));
        });
    }

    /// <summary>
    /// A band is applied per finding by the browser, like the level, and survives the page writing
    /// the address back: a critical-only link that came back without its band would draw every
    /// finding.
    /// </summary>
    [Fact]
    public void ABandReachesTheBrowserFilterAndStaysInTheAddress()
    {
        var module = StubGraphModule();
        Arrive("scope=owned&securitySeverity=critical%2Chigh");

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
            Assert.Equal(["critical", "high"], Field<string[]>(LastFilter(module), "bands")));
        page.Instance.SyncAddress();
        Assert.Contains("securitySeverity=critical%2Chigh", CurrentQuery());
    }

    /// <summary>
    /// A term the graph refuses is refused here too, rather than drawn around: the whole scope's
    /// picture under a chip naming one source is the wider answer a refusal exists to prevent.
    /// </summary>
    [Theory]
    [InlineData("scope=owned&source=custom", "Source")]
    [InlineData("scope=owned&repositories=octo-org%2Fapp", "Finding repository")]
    public void ATermTheGraphRefusesIsSaidAndNothingIsDrawn(string query, string label)
    {
        var module = StubGraphModule();
        Arrive(query);

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
            Assert.Contains(label, page.Find("[data-abom-graph-refused]").TextContent));
        Assert.Empty(module.Invocations["loadFull"]);
    }

    /// <summary>
    /// Silence means the question was not asked, on every consumer. This page used to read an
    /// absent <c>flags</c> as <c>findings</c>, so <c>?scope=owned</c> and the "What you own" chip
    /// drew two different pictures of one predicate, and "every flag off" could not be shared.
    /// </summary>
    [Fact]
    public void AnAbsentFlagsTermAsksNothing()
    {
        var module = StubGraphModule();
        Arrive("scope=owned");

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.False(Field<bool>(LastFilter(module), "hasFindings")));
        page.Instance.SyncAddress();
        Assert.DoesNotContain("flags=", CurrentQuery());
    }

    [Fact]
    public void RemovingTheReferenceStyleChipTakesTheTermAway()
    {
        var module = StubGraphModule();
        Arrive("scope=owned&ref=tag");
        Render<MudBlazor.MudPopoverProvider>();
        var page = Render<Explorer>();

        page.WaitForElement("[data-abom-filter-chip='ref'] [data-abom-chip-remove]").Click();

        page.WaitForAssertion(() => Assert.DoesNotContain("ref=", CurrentQuery()));
        Assert.Equal("any", Field<string>(LastFilter(module), "refStyle"));
    }

    /// <summary>
    /// Closing the scope chip clears the scope, and what is left is drawn the way a named scope
    /// without one already is. It used to come straight back, so the close button did nothing.
    /// </summary>
    [Fact]
    public void RemovingTheScopeChipTakesTheScopeAway()
    {
        var module = StubGraphModule();
        Arrive("scope=owned&flags=findings");
        Render<MudBlazor.MudPopoverProvider>();
        var page = Render<Explorer>();

        page.WaitForElement("[data-abom-filter-chip='scope'] [data-abom-chip-remove]").Click();

        page.WaitForAssertion(() => Assert.DoesNotContain("scope=", CurrentQuery()));
        var url = (string)module.Invocations["loadFull"].Last().Arguments[1]!;
        Assert.DoesNotContain("scope=", url);
    }

    /// <summary>With nothing left in the scope nothing is asked, and the page says to choose.</summary>
    [Fact]
    public void RemovingTheLastTermLeavesNothingAsked()
    {
        var module = StubGraphModule();
        Arrive("scope=owned");
        Render<MudBlazor.MudPopoverProvider>();
        var page = Render<Explorer>();
        page.WaitForAssertion(() => Assert.Single(module.Invocations["loadFull"]));

        page.WaitForElement("[data-abom-filter-chip='scope'] [data-abom-chip-remove]").Click();

        page.WaitForElement("[data-abom-no-scope]");
        Assert.Single(module.Invocations["loadFull"]);
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
