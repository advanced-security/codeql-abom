using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A bill of materials rooted at one repository is an action on that repository's row, and it
/// states what the document covers before anybody downloads it: how much was examined, how deep
/// it goes, and that not examined is not none found.
/// </summary>
public sealed class InventoryDocumentTests : AbomTestContext
{
    private const string Repositories = """
        {"items":[{"fullName":"octo-org/billing","origin":"seed","workflows":4,
          "dependencies":37,"findings":9,"exposures":3,"directExposures":1},
          {"fullName":"octo-org/quiet","origin":"seed","workflows":0,
          "dependencies":0,"findings":0,"exposures":0,"directExposures":0}],"total":2}
        """;

    private const string Summary = """
        {"roots":["octo-org/billing"],"generatedAt":"2026-09-28T00:00:00Z",
         "coverage":{"componentsInUse":270,"componentsAnalyzed":178,"referencesInUse":929,
           "referencesAnalyzed":786,"findings":2198,"findingsUntraced":61,
           "findingsWithoutHash":113,"findingsWithoutDigest":113,"findingsWithoutProvider":1655},
         "direct":129,"indirect":141,"deepestHop":9,"withFindings":60,"noneFound":118,
         "notExamined":92}
        """;

    private const string EmptySummary = """
        {"roots":["octo-org/quiet"],"generatedAt":"2026-09-28T00:00:00Z",
         "coverage":{"componentsInUse":0,"componentsAnalyzed":0,"referencesInUse":0,
           "referencesAnalyzed":0,"findings":0,"findingsUntraced":0,"findingsWithoutHash":0,
           "findingsWithoutDigest":0,"findingsWithoutProvider":0},
         "direct":0,"indirect":0,"deepestHop":0,"withFindings":0,"noneFound":0,"notExamined":0}
        """;

    public InventoryDocumentTests()
    {
        Answer("api/views", "[]");
        Answer("api/repositories", Repositories);
        Answer("api/actions", """{"items":[],"total":0}""");
    }

    private IRenderedComponent<Pages.Inventory> Open(string repository, string summary)
    {
        Answer("api/bom", summary);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?unit=repositories");
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => page.Find(
            $"[data-abom-row-action='bill-of-materials'][aria-label*='{repository}']").Click());
        return page;
    }

    [Fact]
    public void ARepositoryRowOffersABillOfMaterialsRootedAtIt()
    {
        var page = Open("octo-org/billing", Summary);

        page.WaitForAssertion(() =>
        {
            Assert.Contains(Sent, r => r.Url.Contains("api/bom?root=octo-org%2Fbilling&format=summary"));
            var links = page.FindAll("[data-abom-document] a[href*='api/bom']")
                .Select(a => a.GetAttribute("href") ?? "").ToList();
            Assert.Equal(3, links.Count);
            Assert.All(links, href => Assert.Contains("root=octo-org%2Fbilling", href));
        });
    }

    [Fact]
    public void TheDocumentStatesItsCoverageDepthAndWhatWasNotExamined()
    {
        var page = Open("octo-org/billing", Summary);

        page.WaitForAssertion(() =>
        {
            var text = page.Find("[data-abom-document]").TextContent;
            Assert.Contains("178 of 270 components analyzed", text);
            Assert.Contains("786 of 929 references", text);
            Assert.Contains("129 direct", text);
            Assert.Contains("141 indirect", text);
            Assert.Contains("up to 9 hops", text);
            Assert.Contains("92 not examined", text);
            Assert.Contains("118 examined and none found", text);
            Assert.Contains("61 not traceable to a run", text);
            Assert.Contains("113 with no result hash", text);
            Assert.Contains("113 with no pack digest", text);
            Assert.Contains("not evaluated", text);
        });
    }

    /// Nothing read from a repository's workflows leaves an empty document, which a download would
    /// present as a repository that depends on nothing.
    [Fact]
    public void AnEmptyDocumentForARepositoryNothingReadIsRefusedRatherThanOffered()
    {
        var page = Open("octo-org/quiet", EmptySummary);

        page.WaitForAssertion(() =>
        {
            var document = page.Find("[data-abom-document]");
            Assert.Contains("Nothing has been read from octo-org/quiet", document.TextContent);
            Assert.DoesNotContain("0 of 0", document.TextContent);
            Assert.Empty(page.FindAll("[data-abom-document] a[href*='api/bom']"));
            Assert.NotNull(page.Find("[data-abom-document] [data-abom-document-refused]"));
        });
    }

    /// Relationships and Coverage used to borrow the Actions unit's description, which told a
    /// reader looking at lines in files or at the crawl queue that they were looking at actions.
    [Theory]
    [InlineData("relationships", "declares")]
    [InlineData("coverage", "crawl")]
    public void EachUnitDescribesItsOwnRows(string unit, string phrase)
    {
        Answer("api/relationships", """{"items":[],"total":0}""");
        Answer("api/frontier", "[]");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");
        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var header = page.Find("[data-abom-unit-description]").TextContent;
            Assert.DoesNotContain("widest reach first", header);
            Assert.Contains(phrase, header);
        });
    }
}
