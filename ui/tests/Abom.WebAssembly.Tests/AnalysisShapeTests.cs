using System.Linq;
using Bunit;
using MudBlazor;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Findings state the analysis shape they were seen in. A result from a repository's own checkout
/// and a result from its staged closure are not interchangeable, and a reader must not have to
/// discover a filter before being told which one is on the screen.
/// </summary>
public class AnalysisShapeTests : AbomTestContext
{
    private const string Document = """
        {"roots":["octo-org/app"],"generatedAt":"2026-09-07T00:00:00Z",
         "components":[
           {"key":"actions/checkout","kind":"action","repository":"actions/checkout",
            "direct":true,"depth":1,"analyzed":true,"refsInUse":1,"refsAnalyzed":1,
            "references":[{"raw":"actions/checkout@v4","value":"v4","analyzed":true}],
            "findings":[{"ruleId":"actions/unpinned-tag","repository":"octo-org/app",
                         "runKey":"run-1","analysisModes":["regular"]}],
            "advisories":[]},
           {"key":"actions/cache","kind":"action","repository":"actions/cache",
            "direct":true,"depth":1,"analyzed":true,"refsInUse":1,"refsAnalyzed":1,
            "references":[{"raw":"actions/cache@v4","value":"v4","analyzed":true}],
            "findings":[{"ruleId":"actions/unpinned-tag","repository":"octo-org/app",
                         "runKey":"run-2","analysisModes":["extended","regular"]},
                        {"ruleId":"actions/code-injection","repository":"octo-org/app",
                         "runKey":""}],
            "advisories":[]}],
         "relationships":[],
         "coverage":{"componentsInUse":2,"componentsAnalyzed":2,"referencesInUse":2,
                     "referencesAnalyzed":2,"findings":3,"findingsUntraced":1,
                     "findingsWithoutHash":0,"findingsWithoutDigest":0}}
        """;

    private const string Properties = """
        {"configured":"business_id","keys":["business_id"],
         "breakdown":{"name":"business_id","repositories":12,"withValue":12,"withoutValue":0,
           "values":[{"value":"0","repositories":8,"isDefault":true},
                     {"value":"3","repositories":4,"isDefault":false}],
           "hasDefault":true,"defaultValue":"0"}}
        """;

    /// <summary>
    /// The shape is on the page rather than behind a filter, because a reader who does not know
    /// the distinction exists is exactly the reader who would mistake one for the other.
    /// </summary>
    [Fact]
    public void AFindingNamesTheShapeItWasSeenIn()
    {
        Answer("api/bom", Document);
        Answer("api/properties", Properties);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("regular", cut.Markup));
    }

    /// <summary>
    /// One defect two analyses both found is one defect. Labelled `mixed` rather than listed
    /// twice, because a count that splits it would report a difference in how it was looked for
    /// as a difference in what is wrong.
    /// </summary>
    [Fact]
    public void ADefectSeenInBothShapesIsLabelledMixed()
    {
        Answer("api/bom", Document);
        Answer("api/properties", Properties);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("mixed", cut.Markup));
    }

    /// <summary>
    /// A finding no run claims has no shape to state, and saying `regular` would be a guess
    /// wearing the clothes of provenance.
    /// </summary>
    [Fact]
    public void AFindingWithNoRecordedShapeSaysSoRatherThanGuessing()
    {
        Answer("api/bom", Document);
        Answer("api/properties", Properties);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("shape not recorded", cut.Markup));
    }

    /// <summary>
    /// A listing that declares it can narrow by shape has to ask the server for the shape the
    /// address names. Carried rather than offered as a control: a term serialized into a link and
    /// then dropped on the next request is the silent widening the shared predicate exists to
    /// prevent, and it answers under the name of a question nobody asked.
    /// </summary>
    [Fact]
    public void AListingAsksTheServerForTheShapeTheAddressNames()
    {
        Answer("api/views", """[]""");
        Answer("api/actions", """{"items":[],"total":0}""");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?mode=extended");

        var cut = Render<Pages.Inventory>();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        Assert.Contains("mode=extended",
            Sent.First(r => r.Url.Contains("api/actions")).Url);
    }

    [Fact]
    public async Task ChoosingAnAnalysisShapeReloadsTheListingAndKeepsItsScope()
    {
        Answer("api/views", """[]""");
        Answer("api/actions", """{"items":[],"total":0}""");
        Answer("api/properties", Properties);
        var navigation = Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!;
        navigation.NavigateTo("/inventory?rules=actions%2Funpinned-tag");

        var page = Render<Pages.Inventory>();
        var control = page.FindComponent<Shared.ScopeControl>()
                          .FindComponent<MudToggleGroup<string>>();
        Assert.Equal("regular", control.Instance.Value);

        await page.InvokeAsync(() => control.Instance.ValueChanged.InvokeAsync("extended"));

        page.WaitForAssertion(() =>
        {
            var request = Sent.Last(r => r.Url.Contains("api/actions")).Url;
            Assert.Contains("mode=extended", request);
            Assert.Contains("rules=actions%2Funpinned-tag", request);
            Assert.Contains("mode=extended", navigation.Uri);
            Assert.Contains("Coverage and dependency reach use regular analysis.", page.Markup);
        });
    }

    [Fact]
    public void AnExtendedExportNeverSilentlyBecomesARegularDocument()
    {
        Answer("api/views", """[]""");
        Answer("api/actions", """{"items":[],"total":0}""");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?mode=extended");

        var page = Render<Pages.Inventory>();

        foreach (var format in new[] { "cyclonedx", "spdx" })
        {
            Assert.Contains("mode=extended",
                page.Find($"[data-abom-action='bom-{format}']").GetAttribute("href"));
        }
    }

    [Fact]
    public void ABillOfMaterialsRefusesAnAnalysisShapeItCannotDescribe()
    {
        Fail("api/bom?mode=extended", System.Net.HttpStatusCode.BadRequest);
        Answer("api/bom", Document);
        Answer("api/properties", Properties);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/bom?mode=extended");

        var page = Render<Pages.BillOfMaterials>();

        page.WaitForAssertion(() => Assert.Contains("does not narrow by", page.Markup));
        Assert.Contains("mode", page.Markup);
        Assert.Contains(Sent, request => request.Url.Contains("api/bom?mode=extended"));
    }

    /// <summary>
    /// A document the endpoint refuses to narrow must not be downloadable under a narrowing it
    /// does not apply. A link built without the term answers 200 with a regular document at an
    /// address that names another shape, which is a silent widening rather than a refusal.
    ///
    /// Asked with `regular` because that is the shape whose document renders: the endpoint
    /// refuses the term whatever its value, so with `extended` the page shows the refusal and no
    /// buttons at all.
    /// </summary>
    [Fact]
    public void EveryBillOfMaterialsDownloadCarriesTheShapeTheAddressNames()
    {
        Answer("api/bom", Document);
        Answer("api/properties", Properties);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/bom?mode=regular");

        var page = Render<Pages.BillOfMaterials>();

        page.WaitForAssertion(() => Assert.Equal(3, page.FindAll("a[href*='api/bom']").Count));
        foreach (var link in page.FindAll("a[href*='api/bom']"))
        {
            Assert.Contains("mode=regular", link.GetAttribute("href"));
        }
    }

    /// <summary>
    /// The graph applies the shape itself rather than handing it to the browser, so the term has
    /// to reach the request. The Explorer assembles that URL by hand from its own controls, and a
    /// term it offers no control for is dropped there — which does not narrow the picture, it
    /// silently swaps it for a different one drawn under the same address.
    /// </summary>
    [Fact]
    public async Task TheGraphIsRequestedForTheShapeTheAddressNames()
    {
        Answer("api/owned-scope", """[{"owner":"octo-org"}]""");
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Pages.Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Pages.Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Pages.Explorer.Counts>("applyFilters", _ => true)
              .SetResult(new Pages.Explorer.Counts(0, 0, 0, 0));
        foreach (var name in new[]
                 {
                     "setPalette", "setAnimate", "setGroupBy", "setCollapsed", "setView",
                 })
        {
            module.SetupVoid(name, _ => true).SetVoidResult();
        }
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?mode=extended&scope=owned");

        var page = Render<Pages.Explorer>();

        page.WaitForAssertion(() => Assert.NotEmpty(JSInterop.Invocations["loadFull"]));
        Assert.All(JSInterop.Invocations["loadFull"],
            call => Assert.Contains("mode=extended", (string)call.Arguments[1]!));

        var control = page.FindComponent<Shared.ScopeControl>()
                  .FindComponent<MudToggleGroup<string>>();
        Assert.Equal("extended", control.Instance.Value);
        var requests = JSInterop.Invocations["loadFull"].Count;
        await page.InvokeAsync(() => control.Instance.ValueChanged.InvokeAsync("regular"));

        page.WaitForAssertion(() =>
        {
            Assert.True(JSInterop.Invocations["loadFull"].Count > requests);
            Assert.DoesNotContain("mode=extended",
            (string)JSInterop.Invocations["loadFull"].Last().Arguments[1]!);
        });
    }

    /// <summary>
    /// Following is the same question asked again, so it has to carry the same terms. A delta
    /// built from `since` alone answers for the default shape, which merges regular arrivals into
    /// an extended picture and leaves the address claiming something the graph no longer shows.
    /// </summary>
    [Fact]
    public void TheGraphFollowsInTheShapeItWasDrawnIn()
    {
        Answer("api/owned-scope", """[{"owner":"octo-org"}]""");
        Answer("api/graph/version", """{"generation":9,"nodes":0,"edges":0}""");
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        // Cached, so the page holds a cursor and catches up rather than loading afresh.
        module.Setup<Pages.Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Pages.Explorer.LoadResult(0, 0, [], true, 7));
        module.Setup<Pages.Explorer.MergeResult>("mergeDelta", _ => true)
              .SetResult(new Pages.Explorer.MergeResult(false, 9, 0, 0, []));
        module.Setup<Pages.Explorer.Counts>("applyFilters", _ => true)
              .SetResult(new Pages.Explorer.Counts(0, 0, 0, 0));
        foreach (var name in new[]
                 {
                     "setPalette", "setAnimate", "setGroupBy", "setCollapsed", "setView",
                     "setGeneration",
                 })
        {
            module.SetupVoid(name, _ => true).SetVoidResult();
        }
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?mode=extended&scope=owned");

        var page = Render<Pages.Explorer>();

        page.WaitForAssertion(() => Assert.NotEmpty(JSInterop.Invocations["mergeDelta"]));
        Assert.All(JSInterop.Invocations["mergeDelta"], call =>
        {
            var url = (string)call.Arguments[1]!;
            Assert.Contains("mode=extended", url);
            Assert.Contains("scope=owned", url);
        });
    }
}
