using Abom.WebAssembly.Models;
using Abom.WebAssembly.Pages;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Every count of findings says at what severity, in the one banding the Explorer uses. A count on
/// its own put 36 critical findings in the same column as 11,998 medium ones.
/// </summary>
public class SeverityEverywhereTests : AbomTestContext
{
    private const string Views = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]
        """;

    private const string Dependencies = """
        {"items":[{"nodeKey":"actions/checkout","kind":"action","origin":"external",
          "dependentRepos":50,"dependentSteps":60,"distinctRefs":3,
          "findings":3,"severity":{"critical":1,"high":0,"medium":2,"low":0,"unknown":0},
          "advisories":0,"analyzed":true,"refs":["v4"]}],"total":1}
        """;

    private const string Relationships = """
        {"items":[{"edgeKey":"octo-org/app:.github/workflows/ci.yml:20:15",
          "kind":"step-uses-action","source":"step:octo-org/app:ci.yml::1",
          "target":"actions/setup-node","ref":"v4","refStyle":"tag",
          "boundary":"cross-owner","repository":"octo-org/app",
          "filePath":".github/workflows/ci.yml","startLine":20,
          "findings":1,"severity":{"critical":0,"high":0,"medium":1,"low":0,"unknown":0},
          "rules":["actions/unpinned-tag"],
          "advisoryRoute":false,"findingRoute":true,"ownedDepth":1}],"total":1}
        """;

    private IRenderedComponent<Inventory> RenderInventory()
    {
        Answer("api/views", Views);
        Answer("api/actions", Dependencies);
        Answer("api/relationships", Relationships);
        var page = Render<Inventory>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        return page;
    }

    [Fact]
    public void ADependencyShowsFindingSeveritiesAsCompactLabelledIcons()
    {
        var page = RenderInventory();

        page.WaitForAssertion(() =>
        {
            var bands = page.FindAll("[data-abom-severity-icon]");
            Assert.Equal(["critical", "medium"], bands.Select(b => b.GetAttribute("data-abom-severity-icon")));
            Assert.Equal("1 critical finding", bands[0].GetAttribute("aria-label"));
            Assert.Equal("2 medium findings", bands[1].GetAttribute("aria-label"));
            Assert.DoesNotContain("1 critical \u00b7 2 medium", page.Markup);
        });
    }

    [Fact]
    public void ADependencyHasNoSeverityOfItsOwnAndSortsByFindingSeverity()
    {
        var page = RenderInventory();

        page.WaitForAssertion(() =>
            Assert.DoesNotContain(page.FindAll(".mud-table-head th"), h => h.TextContent.Trim() == "Severity"));
        page.FindAll(".sortable-column-header")
            .First(h => h.TextContent.Contains("Findings")).Click();
        page.WaitForAssertion(() =>
            Assert.Contains(Sent, r => r.Url.Contains("api/actions") && r.Url.Contains("sort=findings")));
    }

    [Fact]
    public void ARelationshipsFindingsCountSaysAtWhatSeverity()
    {
        var page = RenderInventory();
        page.Find("[data-abom-unit='relationships']").Click();

        page.WaitForAssertion(() =>
            Assert.Equal("1 medium finding", page.Find("[data-abom-severity-icon='medium']").GetAttribute("aria-label")));
    }

    private const string NodeDetail = """
        {"key":"actions/cache","kind":"action","repository":"","origin":"","path":"",
         "isExternal":true,"analyzed":true,"dependentRepos":2,
         "uses":[],
         "rules":[
           {"ruleId":"actions/code-injection/critical","count":1,"sources":["upstream"],"unsourced":0,
            "severity":{"critical":1,"high":0,"medium":0,"low":0,"unknown":0}},
           {"ruleId":"actions/unnecessary-use-of-advanced-config","count":2,"sources":[],"unsourced":2,
            "severity":{"critical":0,"high":0,"medium":0,"low":0,"unknown":2}}]}
        """;

    [Fact]
    public void ANodesRulesEachSayTheirBandAndTheTotalSaysTheSplit()
    {
        Answer("/-/uses", """{"key":"actions/cache","total":0,"items":[]}""");
        Answer("/-/versions", """{"key":"actions/cache","total":0,"items":[],"advisories":[]}""");
        Answer("api/node/", NodeDetail);

        var cut = Render<NodePage>(p => p.Add(c => c.Key, "actions/cache"));

        cut.WaitForAssertion(() => cut.Find("[data-abom-rule='actions/code-injection/critical']"));
        Assert.Contains("Critical",
            cut.Find("[data-abom-rule='actions/code-injection/critical'] [data-abom-rule-band]").TextContent);
        Assert.Contains("Unknown",
            cut.Find("[data-abom-rule='actions/unnecessary-use-of-advanced-config'] [data-abom-rule-band]").TextContent);
        Assert.Equal("1 critical \u00b7 2 unknown", cut.Find("[data-abom-node-severity]").TextContent.Trim());
    }

    private const string CatalogRows = """
        {"queries":[
          {"queryId":"actions/untrusted-checkout/critical","queryVersion":"c1c1c1c1c1c1","pack":"codeql/actions-queries",
           "packVersion":"0.6.36","cliVersion":"2.27.0","source":"upstream","kind":"path-problem","kindRecorded":true,
           "stage":"","stagedBy":"","stagedAt":"","runs":0,"repositories":0,"firstRunAt":"","lastRunAt":"",
           "band":"critical","steps":[],"requests":[],
           "metadata":{"id":"actions/untrusted-checkout/critical","kind":"path-problem","problem.severity":"error",
                       "security-severity":"9.3","precision":"very-high"}},
          {"queryId":"actions/never-resolved","queryVersion":"n1n1n1n1n1n1","pack":"codeql/actions-queries",
           "packVersion":"0.6.36","cliVersion":"2.27.0","source":"upstream","kind":"problem","kindRecorded":true,
           "stage":"","stagedBy":"","stagedAt":"","runs":1,"repositories":1,"firstRunAt":"","lastRunAt":"2026-09-22T16:20:00Z",
           "steps":[],"requests":[]}
        ],"stages":["dev","test","extended-test","review","deploy"]}
        """;

    /// <summary>
    /// A version says what it would report at before anything runs it, and what it declares is on
    /// the row whole. One nothing resolved says so rather than reading as a query declaring nothing.
    /// </summary>
    [Fact]
    public void ACatalogVersionSaysItsDeclaredSeverityAndEverythingElseItDeclares()
    {
        Answer("api/queries", CatalogRows);
        Answer("api/accounts", """{"authenticated":true,"login":"operator","host":"github.com","accounts":[],"canSeeAllOrganizations":true}""");
        Fallback("api/runs", """{"runs":[]}""");
        Fallback("api/queries/", "[]");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/codeql?tab=catalog");
        var page = Render<CodeQL>();
        page.WaitForAssertion(() => page.Find("[data-abom-query-band='actions/untrusted-checkout/critical']"));

        var band = page.Find("[data-abom-query-band='actions/untrusted-checkout/critical']").TextContent;
        Assert.Contains("Critical", band);
        Assert.Contains("error", band);
        Assert.Contains("9.3", band);
        Assert.NotNull(page.Find("[data-abom-query-metadata-absent='actions/never-resolved']"));

        page.FindAll("[data-abom-query-detail]")[0].Click();
        page.WaitForAssertion(() =>
        {
            var declared = page.Find("[data-abom-query-metadata]").TextContent;
            Assert.Contains("precision", declared);
            Assert.Contains("very-high", declared);
        });
    }

    /// <summary>
    /// Severity has its own column. Folded under the query's name it could not be sorted, and the
    /// name was harder to read.
    /// </summary>
    [Fact]
    public void ACatalogQuerysSeverityHasItsOwnSortableColumn()
    {
        Answer("api/queries", CatalogRows);
        Answer("api/accounts", """{"authenticated":true,"login":"operator","host":"github.com","accounts":[],"canSeeAllOrganizations":true}""");
        Fallback("api/runs", """{"runs":[]}""");
        Fallback("api/queries/", "[]");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/codeql?tab=catalog");
        var page = Render<CodeQL>();
        page.WaitForAssertion(() => page.Find("[data-abom-query-band='actions/untrusted-checkout/critical']"));

        var severity = page.FindAll("[data-abom-queries-table] thead th")
            .Single(th => th.TextContent.Trim() == "Severity");
        Assert.NotNull(severity.QuerySelector(".sortable-column-header"));
        foreach (var id in new[] { "actions/untrusted-checkout/critical", "actions/never-resolved" })
        {
            var name = page.Find($"[data-abom-query-id='{id}']").Closest("td")!;
            Assert.Null(name.QuerySelector("[data-abom-query-band], [data-abom-query-metadata-absent]"));
        }
    }

    private const string CatalogRowsTheServerExplains = """
        {"queries":[
          {"queryId":"acme/self-hosted-runner","queryVersion":"9b5327ab01d7","pack":"abom/custom-queries",
           "packVersion":"2.27.0","cliVersion":"2.27.0","source":"custom","kind":"problem","kindRecorded":true,
           "stage":"","stagedBy":"","stagedAt":"","runs":57,"repositories":57,"firstRunAt":"","lastRunAt":"",
           "steps":[],"requests":[],
           "metadataUnread":"Declared metadata cannot be read: abom/custom-queries 2.27.0 is not on disk."}
        ],"stages":["dev","test","extended-test","review","deploy"]}
        """;

    /// <summary>
    /// Why a version's declaration is missing is the server's to say, and the reasons are not
    /// alike: one being read now, one whose file is gone and one nobody versioned. Printing
    /// <em>not read yet</em> over all three promises a read that, for two of them, never comes.
    /// </summary>
    [Fact]
    public void ACatalogVersionWithoutADeclarationSaysWhyInTheServersWords()
    {
        Answer("api/queries", CatalogRowsTheServerExplains);
        Answer("api/accounts", """{"authenticated":true,"login":"operator","host":"github.com","accounts":[],"canSeeAllOrganizations":true}""");
        Fallback("api/runs", """{"runs":[]}""");
        Fallback("api/queries/", "[]");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/codeql?tab=catalog");
        var page = Render<CodeQL>();

        page.WaitForAssertion(() => page.Find("[data-abom-query-metadata-absent='acme/self-hosted-runner']"));
        var cell = page.Find("[data-abom-query-metadata-absent='acme/self-hosted-runner']");
        Assert.Contains("abom/custom-queries 2.27.0 is not on disk", cell.GetAttribute("title"));
        // The sentence in the cell set every row's height, and a catalog row is one line of data.
        Assert.Equal("cannot be read", cell.TextContent.Trim());
    }

    private const string RunWithFindings = """
        {"rowId":3,"predicate":"repositories=octo%2Fapp","mode":"regular",
         "requestedBy":"operator","state":"complete","createdAt":"2026-09-22T16:00:00Z",
         "finishedAt":"2026-09-22T16:04:00Z","results":2,
         "queries":[{"queryId":"acme/rule","queryVersion":"v1"}],
         "tally":{"succeeded":1},
         "members":[{"repository":"octo/app","status":"succeeded","counted":true,"results":2,
            "byQuery":{"acme/rule":2}}]}
        """;

    private const string MemberFindings = """
        {"repository":"octo/app","evaluated":true,"truncated":false,"findings":[
          {"findingRowId":41,"ruleId":"acme/rule","filePath":".github/workflows/ci.yml","startLine":6,
           "startColumn":5,"message":"a job","severity":"error","securitySeverity":9.1,"band":"critical"},
          {"findingRowId":42,"ruleId":"acme/rule","filePath":".github/workflows/deploy.yml","startLine":9,
           "startColumn":5,"message":"a job","severity":"warning","band":"unknown"}]}
        """;

    [Fact]
    public void AResultARunReportedSaysItsBand()
    {
        Answer("api/runs/3/results", MemberFindings);
        Answer("api/runs/3", RunWithFindings);
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 3L).Add(c => c.QueryId, "acme/rule"));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));
        cut.Find("[data-abom-results-pick]").Click();
        cut.Find("[data-abom-member-findings='octo/app']").Click();

        cut.WaitForAssertion(() =>
        {
            Assert.Contains("Critical", cut.Find("[data-abom-finding-band='41']").TextContent);
            Assert.Contains("Unknown", cut.Find("[data-abom-finding-band='42']").TextContent);
        });
    }

    [Fact]
    public void TheOwnedFindingsCountSaysAtWhatSeverity()
    {
        Answer("api/summary", """
            {"ownedRepos":3,"ownedFindings":4,"findings":9,"rules":2,
             "ownedSeverity":{"critical":1,"high":0,"medium":2,"low":0,"unknown":1}}
            """);

        var page = Render<Home>();

        page.WaitForAssertion(() =>
            Assert.Equal("1 critical \u00b7 2 medium \u00b7 1 unknown",
                page.Find("[data-abom-entry-severity]").TextContent.Trim()));
    }

    /// <summary>
    /// The summary reads the band the server sent. A rule declaring no score is unknown, never the
    /// level its result reported: a recommendation is not a severity band.
    /// </summary>
    [Fact]
    public void ASummaryNamesBandsMostSevereFirstAndUnknownLast()
    {
        var text = SeverityText.Summary(new List<FindingFacet>
        {
            new() { Rule = "c", Severity = "recommendation", Band = "unknown", Count = 1 },
            new() { Rule = "a", Severity = "warning", Band = "medium", Count = 3 },
            new() { Rule = "b", Severity = "error", Band = "critical", Count = 1 },
        });

        Assert.Equal("1 critical \u00b7 3 medium \u00b7 1 unknown", text);
    }
}
