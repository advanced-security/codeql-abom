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

        var popover = Render<MudBlazor.MudPopoverProvider>();
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));

        // The shape is a term like any other now, so it is chosen the way every term is.
        await page.InvokeAsync(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='mode']").Click();
        popover.WaitForElement("[data-abom-value='extended']").Click();

        page.WaitForAssertion(() =>
        {
            var request = Sent.Last(r => r.Url.Contains("api/actions")).Url;
            Assert.Contains("mode=extended", request);
            Assert.Contains("rules=actions%2Funpinned-tag", request);
            Assert.Contains("mode=extended", navigation.Uri);
            Assert.Contains("Coverage and dependency reach use regular analysis.", page.Markup);
        });
    }

    /// <summary>
    /// A document the endpoint refuses to narrow must not be offered under a narrowing it does not
    /// apply. It used to be offered carrying the term, so the refusal arrived as a failed
    /// download; the control refuses first now, with the term that cannot be applied named.
    /// </summary>
    [Fact]
    public void AnExtendedExportIsRefusedRatherThanOfferedAsADocumentItCannotBe()
    {
        Answer("api/views", """[]""");
        Answer("api/actions", """{"items":[],"total":0}""");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?mode=extended");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var reason = page.Find("[data-abom-action-reason='bom']");
            Assert.Contains("Analysis shape", reason.GetAttribute("title") ?? "");
            Assert.Contains("disabled", page.Find("[data-abom-action='bom-spdx']").OuterHtml);
        });
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

        // The shape reads as a chip now, and clearing it is how the graph goes back to regular.
        var requests = JSInterop.Invocations["loadFull"].Count;
        await page.InvokeAsync(() =>
            page.Find("[data-abom-filter-chip='mode'] [data-abom-chip-remove]").Click());

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
