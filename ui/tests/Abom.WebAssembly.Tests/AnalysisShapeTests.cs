using System.Linq;
using Bunit;
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

    /// <summary>
    /// The graph applies the shape itself rather than handing it to the browser, so the term has
    /// to reach the request. The Explorer assembles that URL by hand from its own controls, and a
    /// term it offers no control for is dropped there — which does not narrow the picture, it
    /// silently swaps it for a different one drawn under the same address.
    /// </summary>
    [Fact]
    public void TheGraphIsRequestedForTheShapeTheAddressNames()
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
                .NavigateTo("/explorer?mode=extended&scope=owned");

        Render<Pages.Explorer>();

        Assert.All(JSInterop.Invocations["loadFull"],
            call => Assert.Contains("mode=extended", (string)call.Arguments[1]!));
    }
}
