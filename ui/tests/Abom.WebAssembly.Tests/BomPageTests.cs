using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The bill of materials page. These pin what the document must state about its own limits, which
/// is the difference between a bill of materials and a list.
/// </summary>
public class BomPageTests : AbomTestContext
{
    private const string Document = """
        {"roots":["octo-org/app"],"generatedAt":"2026-09-07T00:00:00Z",
         "components":[
           {"key":"actions/checkout","kind":"action","repository":"actions/checkout",
            "direct":true,"depth":1,"analyzed":true,"refsInUse":1,"refsAnalyzed":1,
            "references":[{"raw":"actions/checkout@v4","value":"v4",
                           "resolvedSha":"cafe1234deadbeef","analyzed":true}],
            "findings":[{"ruleId":"actions/unpinned-tag","repository":"octo-org/app",
                         "runKey":"run-1","packDigest":"sha256:pack",
                         "resultHash":"sha256:result"}],
            "advisories":[]},
           {"key":"actions/setup-go","kind":"action","repository":"actions/setup-go",
            "direct":false,"depth":2,"analyzed":false,"refsInUse":1,"refsAnalyzed":0,
            "references":[{"raw":"actions/setup-go@v5","value":"v5","analyzed":false}],
            "findings":[],"advisories":[
              {"ghsaId":"GHSA-xxxx-yyyy-zzzz","severity":"high","summary":"example",
               "vulnerableRange":"< 4.0.0"}]},
           {"key":"actions/cache","kind":"action","repository":"actions/cache",
            "direct":true,"depth":1,"analyzed":true,"refsInUse":1,"refsAnalyzed":1,
            "references":[{"raw":"actions/cache@v4","value":"v4","analyzed":true}],
            "findings":[],"advisories":[]}],
         "relationships":[
           {"source":"actions/checkout","target":"actions/setup-go",
            "kind":"step-uses-action","boundary":"same-owner"}],
         "coverage":{"componentsInUse":3,"componentsAnalyzed":2,"referencesInUse":3,
                     "referencesAnalyzed":2,"findings":1,"findingsUntraced":1,
                     "findingsWithoutHash":0,"findingsWithoutDigest":0}}
        """;

    private const string Properties = """
        {"configured":"business_id","keys":["business_id"],
         "breakdown":{"name":"business_id","repositories":12,"withValue":12,"withoutValue":0,
           "values":[{"value":"0","repositories":8,"isDefault":true},
                     {"value":"3","repositories":4,"isDefault":false}],
           "hasDefault":true,"defaultValue":"0"}}
        """;

    // The key the operator configured is shown verbatim rather than translated into what it might
    // mean: `business_id` yields business units and `team` yields teams, and ABOM knows which it
    // was told, not which it is. Stated outside the select because MudSelect renders its items only
    // once opened, so anything said only in an item is invisible until a reader goes looking.
    [Fact]
    public void TheConfiguredKeyIsNamedWithoutOpeningAnything()
    {
        Answer("api/bom", Document);
        Answer("api/properties", Properties);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("Grouped by business_id", cut.Markup));
        Assert.Contains("business_id 0 is the organization default", cut.Markup);
        Assert.Contains("not a decision", cut.Markup);
    }

    // The control cannot offer a dividing line it never asked for.
    [Fact]
    public void ThePageAsksHowTheOrganizationIsDividedBeforeItRenders()
    {
        Answer("api/bom", Document);
        Answer("api/properties", Properties);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/properties")));
    }

    // With no key configured the server answers an empty breakdown, and a control offering nothing
    // to choose is worse than no control: it implies the organization divides into nothing.
    [Fact]
    public void NoConfiguredKeyOffersNoGroupControlAtAll()
    {
        Answer("api/bom", Document);
        Answer("api/properties", """
            {"configured":"","keys":[],"breakdown":{"name":"","repositories":0,"withValue":0,
             "withoutValue":0,"values":[],"hasDefault":false,"defaultValue":""}}
            """);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("actions/checkout", cut.Markup));
        Assert.DoesNotContain("Grouped by", cut.Markup);
    }

    // A download that ignored the filter on screen would hand somebody a document about the whole
    // organization under the name of one part of it. The key travels with the value, because a
    // bare value would be read against whatever the server was configured with.
    [Fact]
    public void ADownloadCarriesTheScopeRatherThanTheWholeOrganization()
    {
        var bounded = WebAssembly.Services.AbomApi.BomDownloadUrl(
            "cyclonedx", properties: ["business_id:3"]);

        Assert.Contains("properties=business_id%3A3", bounded);
        Assert.Contains("format=cyclonedx", bounded);
        Assert.DoesNotContain(
            "properties=", WebAssembly.Services.AbomApi.BomDownloadUrl("cyclonedx"));
    }

    // Leaving an unexamined component out would make the document look complete.
    [Fact]
    public void AComponentNothingAnalyzedIsListedAndMarked()
    {
        Answer("api/bom", Document);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("actions/setup-go", cut.Markup));
        Assert.Contains("not analyzed", cut.Markup);
        Assert.Contains("abom-unconfirmed", cut.Markup);
    }

    // "Not examined" and "examined, nothing found" are different answers and must not look alike.
    [Fact]
    public void NothingFoundAndNotExaminedAreWordedDifferently()
    {
        Answer("api/bom", Document);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("not examined", cut.Markup));
        Assert.Contains("none found", cut.Markup);
    }

    // The reference as written is the most useful thing the document carries.
    [Fact]
    public void TheReferenceIsShownAsItWasWritten()
    {
        Answer("api/bom", Document);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("actions/checkout@v4", cut.Markup));
        Assert.Contains("cafe123", cut.Markup);
    }

    // Coverage is on the page, not in a tooltip: a reader must see what was examined.
    [Fact]
    public void CoverageIsStatedOnThePage()
    {
        Answer("api/bom", Document);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("1 not examined", cut.Markup));
        Assert.Contains("not traceable to a run", cut.Markup);
    }

    // Direct and indirect are relative to the roots, and the page must say which it means.
    [Fact]
    public void DirectAndIndirectAreDistinguished()
    {
        Answer("api/bom", Document);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("direct", cut.Markup));
        Assert.Contains("indirect, 2 hops", cut.Markup);
    }

    // An advisory is published against a repository. Saying it applies to the reference in use is
    // a judgment ABOM has not made, and the page must not imply one. Written out rather than left
    // to a tooltip, which a reader who never hovers would never see.
    [Fact]
    public void AnAdvisoryIsReportedWithoutBeingAppliedToAReference()
    {
        Answer("api/bom", Document);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("1 named", cut.Markup));
        Assert.Contains("not evaluated against the references in use", cut.Markup);
    }

    // All three formats reachable, and the native one labelled as the only verifiable option.
    [Fact]
    public void EveryExportFormatIsOffered()
    {
        Answer("api/bom", Document);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("format=native", cut.Markup));
        Assert.Contains("format=cyclonedx", cut.Markup);
        Assert.Contains("format=spdx", cut.Markup);
        Assert.Contains("download=true", cut.Markup);
    }

    // An undeclared scope is the expected first-run state, not an error to bury. Rendering an
    // empty grid would report that the organization depends on nothing.
    [Fact]
    public void AnUndeclaredScopeExplainsItselfRatherThanShowingAnEmptyDocument()
    {
        // Deliberately unstubbed: the harness answers 404, which is what a refused document
        // looks like to the page.
        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("No accounts are declared", cut.Markup));
        Assert.Contains("/settings", cut.Markup);
    }

    // Zero of zero is not full coverage. Checked on the progress bar's own value rather than by
    // searching for "100", which also matches `aria-valuemax`.
    [Fact]
    public void AnEmptyDocumentDoesNotReportCompleteCoverage()
    {
        Answer("api/bom", """
            {"roots":["octo-org/app"],"generatedAt":"2026-09-07T00:00:00Z",
             "components":[],"relationships":[],
             "coverage":{"componentsInUse":0,"componentsAnalyzed":0,"referencesInUse":0,
                         "referencesAnalyzed":0,"findings":0,"findingsUntraced":0,
                         "findingsWithoutHash":0,"findingsWithoutDigest":0}}
            """);

        var cut = Render<Pages.BillOfMaterials>();

        cut.WaitForAssertion(() => Assert.Contains("Showing 0 of 0 components", cut.Markup));
        Assert.Contains("aria-valuenow=\"0\"", cut.Markup);
        Assert.DoesNotContain("aria-valuenow=\"100\"", cut.Markup);
    }
}
