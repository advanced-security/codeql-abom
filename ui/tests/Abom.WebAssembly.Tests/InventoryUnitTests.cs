using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// One scope, reprojected. A repository, a dependency and a relationship are different shapes
/// rather than different scopes, so they are units of one surface rather than pages of their own —
/// and a unit's columns have to be its own, or it draws absence as zero.
/// </summary>
public class InventoryUnitTests : AbomTestContext
{
    private const string Views = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]
        """;

    private const string Dependencies = """
        {"items":[{"nodeKey":"actions/checkout","kind":"action","origin":"external",
          "dependentRepos":50,"dependentSteps":60,"distinctRefs":3,
          "findings":2,"advisories":0,"analyzed":true,"refs":["v4"]}],"total":1585}
        """;

    private const string Repositories = """
        {"items":[{"fullName":"octo-org/billing","origin":"seed","workflows":4,
          "dependencies":37,"findings":9,"severity":{"critical":1,"high":2,"medium":6,"low":0,"unknown":0},
          "properties":[{"name":"team","value":"payments"},{"name":"tier","value":"production"}],
          "exposures":3,"directExposures":1}],"total":193}
        """;

    private const string Properties = """
        {"configured":"business_id","keys":["business_id"],
         "breakdown":{"name":"business_id","repositories":12,"withValue":12,"withoutValue":0,
           "values":[{"value":"0","repositories":8,"isDefault":true},
                     {"value":"3","repositories":4,"isDefault":false}],
           "hasDefault":true,"defaultValue":"0"}}
        """;

    private const string Advisories = """
        {"items":[{"ghsaId":"GHSA-aaaa-bbbb-cccc","cveId":"CVE-2024-1","severity":"high",
          "summary":"A thing","directRepos":2,"transitiveRepos":5,"actions":["actions/checkout"],
          "cvssV3Score":7.5,"hasCvss":true,"epssPercentage":0.1,"hasEpss":true,
          "fixable":true,"rank":4}],"total":3,"unreached":8}
        """;

    private const string Relationships = """
        {"items":[{"edgeKey":"octo-org/app:.github/workflows/ci.yml:20:15",
          "kind":"step-uses-action","source":"step:octo-org/app:ci.yml::1",
          "target":"actions/setup-node","ref":"v4","refStyle":"tag",
          "boundary":"cross-owner","repository":"octo-org/app",
          "filePath":".github/workflows/ci.yml","startLine":20,
          "findings":1,"rules":["actions/unpinned-tag"],
          "advisoryRoute":false,"findingRoute":true,"ownedDepth":1}],"total":37}
        """;

    public InventoryUnitTests()
    {
        Answer("api/views", Views);
        Answer("api/repositories", Repositories);
        Answer("api/actions", Dependencies);
        Answer("api/remediation", Advisories);
        Answer("api/relationships", Relationships);
    }

    private IRenderedComponent<Pages.Inventory> RenderInventory()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        return page;
    }

    [Fact]
    public void TheUnitsOnOfferAreTheOnesWithColumnsOfTheirOwn()
    {
        var page = RenderInventory();

        Assert.NotNull(page.Find("[data-abom-unit='dependencies']"));
        Assert.NotNull(page.Find("[data-abom-unit='repositories']"));
    }

    [Fact]
    public void FindingsAreReviewedInTheInventoryScopeWithoutOpeningANode()
    {
        Answer("api/findings", """
            {"items":[{"id":7,"ruleId":"actions/code-injection/medium",
              "repository":"octo-org/app","filePath":".github/workflows/ci.yml",
              "startLine":20,"message":"Untrusted input reaches a shell command",
              "severity":"warning","securitySeverity":6.0,"provider":"codeql",
              "queryRunId":12,"commit":"abc123","linked":true}],"total":1}
            """);
                Answer("api/findings/7/review", """
                        {"finding":{"id":7,"ruleId":"actions/code-injection/medium","repository":"octo-org/app",
                            "filePath":".github/workflows/ci.yml","startLine":20,"message":"Untrusted input reaches a shell command",
                            "securitySeverity":6,"commit":"abc123","queryRunId":12},
                         "evaluations":[{"queryRunId":12,"commit":"abc123","mode":"regular"}],
                         "selectedEvaluationId":12,"assets":[],"references":[]}
                        """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?unit=findings&repositories=octo-org%2Fapp");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            Assert.Contains(Sent, request => request.Url.Contains("api/findings?")
                && request.Url.Contains("repositories=octo-org%2Fapp"));
            Assert.Contains("Untrusted input reaches a shell command", page.Markup);
            Assert.Contains("actions/code-injection/medium", page.Markup);
            Assert.Contains(".github/workflows/ci.yml:20", page.Markup);
            Assert.DoesNotContain("Missing analysis or graph attribution can leave gaps", page.Markup);
        });
        page.Find("[data-abom-review-finding='7']").Click();
        var details = page.WaitForElement("[data-abom-finding-detail]");
        Assert.Contains("abc123", details.TextContent);
        Assert.Contains("Query documentation", details.TextContent);
        Assert.Contains("not recorded", details.TextContent);
        Assert.Empty(page.FindAll("a[href*='/blob/abc123']"));
        page.Find("[aria-label='Close finding details']").Click();
        Assert.Empty(page.FindAll("[data-abom-finding-detail]"));
    }

    // A result code scanning's analysis produced names that analysis and the CodeQL and pack that ran
    // it; claiming ABOM's own would put another release's result under ABOM's name.
    [Fact]
    public void AFindingCodeScanningAnsweredSaysWhichAnalysisAndCodeQLProducedIt()
    {
        Answer("api/findings", """
            {"items":[{"id":7,"ruleId":"actions/unpinned-tag",
              "repository":"has-ghas/test-repo-3","filePath":".github/workflows/ci.yml",
              "startLine":11,"message":"Unpinned 3rd party Action","severity":"warning",
              "provider":"CodeQL","queryRunId":12,"commit":"085d022","linked":true}],"total":1}
            """);
        Answer("api/findings/7/review", """
            {"finding":{"id":7,"ruleId":"actions/unpinned-tag","repository":"has-ghas/test-repo-3",
                "filePath":".github/workflows/ci.yml","startLine":11,"message":"Unpinned 3rd party Action",
                "commit":"085d022","queryRunId":12},
             "evaluations":[{"queryRunId":12,"commit":"085d022","mode":"regular","cliVersion":"2.23.1",
                "pack":"codeql/actions-queries","packVersion":"0.6.9+6e9f9e38",
                "takenFrom":"github-code-scanning:1895743852"}],
             "selectedEvaluationId":12,"assets":[],"references":[]}
            """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/inventory?unit=findings&repositories=has-ghas%2Ftest-repo-3");

        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.Contains(".github/workflows/ci.yml:11", page.Markup));
        page.Find("[data-abom-review-finding='7']").Click();
        var details = page.WaitForElement("[data-abom-finding-detail]");

        Assert.Contains("code scanning analysis 1895743852", details.TextContent);
        Assert.Contains("2.23.1", details.TextContent);
        Assert.Contains("codeql/actions-queries 0.6.9+6e9f9e38", details.TextContent);
    }

    [Theory]
    [InlineData("dependencies", "actions/checkout", "node")]
    [InlineData("workflows", "octo-org/app/.github/workflows/ci.yml", "node")]
    [InlineData("jobs", "job:octo-org/app:ci.yml:build", "node")]
    [InlineData("repositories", "octo-org/billing", "repository")]
    public void AssetFindingsOpenInPlaceAndKeepTheScope(string unit, string asset, string kind)
    {
        Answer("api/findings", """{"items":[],"total":0}""");
        Answer("api/workflows", """
            {"items":[{"nodeKey":"octo-org/app/.github/workflows/ci.yml","repository":"octo-org/app",
              "path":".github/workflows/ci.yml","findings":2}],"total":1}
            """);
        Answer("api/jobs", """
            {"items":[{"nodeKey":"job:octo-org/app:ci.yml:build","jobId":"build",
              "workflow":"octo-org/app/.github/workflows/ci.yml","findings":2}],"total":1}
            """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}&scope=owned");
        var page = Render<Pages.Inventory>();
        var count = page.WaitForElement($"[data-abom-review-asset='{asset}']");
        Assert.Equal(kind == "repository" ? "9" : "2", count.TextContent.Trim());
        Assert.Empty(count.QuerySelectorAll("svg"));
        count.Click();
        page.WaitForAssertion(() =>
        {
            Assert.NotNull(page.Find("[data-abom-asset-review]"));
            Assert.Contains(Sent, request => request.Url.Contains("api/findings?")
                && request.Url.Contains($"asset={Uri.EscapeDataString(asset)}")
                && request.Url.Contains($"assetKind={kind}") && request.Url.Contains("scope=owned"));
        });
        page.Find("[aria-label='Close asset findings']").Click();
        Assert.Empty(page.FindAll("[data-abom-asset-review]"));
    }

    /// <summary>
    /// Each unit asks its own endpoint. Reprojecting in the browser would mean showing a repository
    /// the columns of a dependency, which is the absent-as-zero screen this replaced.
    /// </summary>
    [Fact]
    public void ChoosingAUnitAsksTheEndpointThatAnswersAboutIt()
    {
        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() =>
            Assert.Contains(Sent, r => r.Url.Contains("api/repositories?")));
    }

    /// <summary>
    /// A repository is not a node, so it gets repository columns. Counting what depends on a
    /// repository would be a measurement nothing made.
    /// </summary>
    [Fact]
    public void TheRepositoriesUnitRendersRepositoryColumns()
    {
        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() =>
        {
            Assert.Contains("octo-org/billing", page.Markup);
            Assert.Contains("Workflows", page.Markup);
            Assert.Contains("Repository Properties", page.Markup);
            Assert.Contains("team: payments", page.Find("[data-abom-repository-properties='octo-org/billing']").TextContent);
            Assert.Contains("tier: production", page.Find("[data-abom-repository-properties='octo-org/billing']").TextContent);
            Assert.Empty(page.FindAll("[data-abom-level]"));
        });
    }

    /// <summary>
    /// Whether Actions code is scanned is read, never inferred: a repository the server classified
    /// says what the reading said and when, and one it did not says "not read", never "off".
    /// </summary>
    [Fact]
    public void TheRepositoriesUnitSaysWhatTheSetupReadingSaidAndNeverGuesses()
    {
        Answer("api/repositories", """
            {"items":[
              {"fullName":"octo-org/billing","origin":"seed","workflows":4,"dependencies":37,"findings":0,
               "severity":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"properties":[],
               "exposures":0,"directExposures":0,
               "codeScanning":{"known":true,"class":"add","actionsScanning":"off","setup":"default",
                 "actionsDatabase":"absent","evidence":"default setup analyzes python without actions",
                 "readAt":"2026-10-05T12:00:00Z","languages":["python"]}},
              {"fullName":"octo-org/legacy","origin":"seed","workflows":1,"dependencies":2,"findings":0,
               "severity":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"properties":[],
               "exposures":0,"directExposures":0}],"total":2}
            """);
        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() =>
        {
            var scanning = page.Find("[data-abom-setup='octo-org/billing:actionsScanning']");
            var title = scanning.GetAttribute("title") ?? "";
            Assert.Contains("Off", scanning.TextContent);
            Assert.StartsWith("No code scanning analysis of this repository's Actions code is on record.", title);
            Assert.Contains("Default setup analyzes python without actions", title);
            Assert.Contains(TimeText.DayAndTime("2026-10-05T12:00:00Z"), title);
            Assert.DoesNotContain("2026-10-05T12", title);
            var setup = page.Find("[data-abom-setup='octo-org/billing:setup']");
            Assert.StartsWith("Default setup is on.", setup.GetAttribute("title"));
            Assert.Equal("default setup", setup.TextContent.Trim());
            Assert.Equal("no Actions database",
                page.Find("[data-abom-setup='octo-org/billing:actionsDatabase']").TextContent.Trim());
            Assert.Contains("Not read", page.Find("[data-abom-setup='octo-org/legacy:actionsScanning']").TextContent);
            Assert.Empty(page.FindAll("[data-abom-setup='octo-org/legacy:setup'], [data-abom-setup='octo-org/legacy:actionsDatabase']"));
        });
    }

    /// <summary>
    /// The three facts share one column. As three, they put the two advisory route columns 158px
    /// past the right-hand edge of a 1280px window, and every header in this grid stays on one line.
    /// </summary>
    [Fact]
    public void ScanningSetupAndDatabaseShareOneColumn()
    {
        Answer("api/repositories", """
            {"items":[
              {"fullName":"octo-org/billing","origin":"seed","workflows":4,"dependencies":37,"findings":0,
               "severity":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"properties":[],
               "exposures":0,"directExposures":0,
               "codeScanning":{"known":true,"class":"covered","actionsScanning":"default","setup":"default",
                 "actionsDatabase":"present","evidence":"default setup analyzes actions",
                 "readAt":"2026-10-05T12:00:00Z","languages":["actions"],"databaseCommit":"abc123"}}],"total":1}
            """);
        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() =>
        {
            var headers = page.FindAll("thead th").Select(h => h.TextContent.Trim()).ToList();
            Assert.Contains("Actions scanning", headers);
            Assert.DoesNotContain("Setup", headers);
            Assert.DoesNotContain("Actions database", headers);
            var cell = page.Find("[data-abom-setup='octo-org/billing:actionsScanning']").Closest("td")!;
            Assert.Contains("Default setup", cell.TextContent);
            Assert.Contains("Actions database", cell.TextContent);
            Assert.NotNull(cell.QuerySelector("[data-abom-setup='octo-org/billing:actionsDatabase']"));
            // Default setup scanning Actions already says which setup it is; saying it twice says nothing.
            Assert.Null(cell.QuerySelector("[data-abom-setup='octo-org/billing:setup']"));
        });
    }

    /// <summary>
    /// A repository outside the declared scope is never read, so its cells say that and nothing
    /// more: a tip that went on to say "not read yet" promised a reading ABOM will never take.
    /// </summary>
    [Fact]
    public void ARepositoryNobodyOwnsIsNeverPromisedAReading()
    {
        Answer("api/repositories", """
            {"items":[
              {"fullName":"pytorch/pytorch","origin":"seed","workflows":0,"dependencies":0,"findings":0,
               "severity":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"properties":[],
               "exposures":0,"directExposures":0,
               "codeScanning":{"known":true,"class":"not-owned","actionsScanning":"not-owned",
                 "setup":"not-owned","actionsDatabase":"not-owned",
                 "evidence":"outside the declared scope, so ABOM does not read it","languages":[]}}],"total":1}
            """);
        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() =>
        {
            var cell = page.Find("[data-abom-setup='pytorch/pytorch:actionsScanning']");
            Assert.Contains("Not owned", cell.TextContent);
            Assert.Contains("mud-text-secondary", cell.ClassList);
            Assert.Equal("Not owned: outside the declared scope, so ABOM never reads its code scanning.",
                cell.GetAttribute("title"));
            Assert.Empty(page.FindAll("[data-abom-setup='pytorch/pytorch:setup'], [data-abom-setup='pytorch/pytorch:actionsDatabase']"));
        });
    }

    /// <summary>
    /// A relationship is a line in a file, not a thing in the corpus. Its columns are the
    /// reference as written and what was reported against the line itself — neither of which any
    /// unit of nodes carries, which is why it had to become a unit rather than a filter.
    /// </summary>
    [Fact]
    public void TheRelationshipsUnitRendersTheLineAndWhatFiredOnIt()
    {
        var page = RenderInventory();
        page.Find("[data-abom-unit='relationships']").Click();

        page.WaitForAssertion(() =>
        {
            Assert.Contains(Sent, r => r.Url.Contains("api/relationships?"));
            Assert.Contains("actions/setup-node", page.Markup);
            Assert.Contains(".github/workflows/ci.yml:20", page.Markup);
            Assert.Contains("actions/unpinned-tag", page.Markup);
            // Ordering is not use, so the list must not present either as the other.
            Assert.Contains("step uses", page.Markup);
            Assert.Contains("Another account", page.Markup);
            Assert.DoesNotContain(">cross-owner<", page.Markup.Replace(" ", "").Replace("\n", ""));
        });
    }

    // A line whose commits in use declared two things is two rows, and each says which commits
    // declared it. Without that, two rows from one line read as a duplicate or as a disagreement.
    [Fact]
    public void TheRelationshipsUnitNamesTheCommitsEachDeclarationWasReadAt()
    {
        Answer("api/relationships", """
            {"items":[
              {"edgeKey":"vendor/comp:action.yml:15:11","kind":"composite-step-uses-action",
               "source":"vendor/comp","target":"actions/cache","ref":"v3","repository":"vendor/comp",
               "filePath":"action.yml","startLine":15,
               "commits":["1111111111111111111111111111111111111111"]},
              {"edgeKey":"vendor/comp:action.yml:15:11","kind":"composite-step-uses-action",
               "source":"vendor/comp","target":"actions/cache","ref":"v4","repository":"vendor/comp",
               "filePath":"action.yml","startLine":15,
               "commits":["2222222222222222222222222222222222222222",
                          "3333333333333333333333333333333333333333"]},
              {"edgeKey":"octo/app:ci.yml:3:1","kind":"step-uses-action","source":"octo/app/ci.yml",
               "target":"actions/checkout","ref":"v4","commits":[]}],
             "total":3,"lines":2,"withoutDeclaration":5}
            """);
        var page = RenderInventory();
        page.Find("[data-abom-unit='relationships']").Click();

        page.WaitForAssertion(() =>
        {
            var declared = page.FindAll("[data-abom-relationship-commits]");
            Assert.Equal(3, declared.Count);

            Assert.Equal("at 111111111111", declared[0].TextContent.Trim());
            Assert.Equal("Declared at commit 1111111111111111111111111111111111111111, which a " +
                "reference in use resolves to.", declared[0].GetAttribute("title"));

            Assert.Equal("at 222222222222 and 1 more", declared[1].TextContent.Trim());
            Assert.Equal("Declared at the 2 commits 2222222222222222222222222222222222222222 and " +
                "3333333333333333333333333333333333333333, which references in use resolve to.",
                declared[1].GetAttribute("title"));

            Assert.Equal("no commit recorded", declared[2].TextContent.Trim());
            Assert.EndsWith(".", declared[2].GetAttribute("title"));

            var lines = page.Find("[data-abom-relationship-lines]").TextContent;
            Assert.Contains("3 declarations", lines);
            Assert.Contains("2 lines", lines);
            Assert.Contains("5 more lines", page.Find("[data-abom-relationship-undeclared]").TextContent);
        });
    }

    // Unscoped, these units list what ABOM has read, and most of it is nobody's here to own.
    [Theory]
    [InlineData("workflows")]
    [InlineData("repositories")]
    public void AnUnscopedUnitDoesNotCallWhatItListsYours(string unit)
    {
        Answer("api/workflows", """{"items":[],"total":0}""");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");
        var page = Render<Pages.Inventory>();

        var caption = page.Find("[data-abom-unit-description]").TextContent;
        Assert.DoesNotContain("you own", caption);
        Assert.DoesNotContain("your repositories", caption);
    }

    // Dependents sits beside Dependencies, and the privilege column is named as a count of jobs.
    [Fact]
    public void WorkflowsListsDependentsBesideDependencies()
    {
        Answer("api/workflows", """
            {"items":[{"nodeKey":"octo-org/shared/.github/workflows/build.yml","kind":"reusable_workflow",
              "repository":"octo-org/shared","path":".github/workflows/build.yml",
              "jobs":1,"steps":2,"dependencies":3,"dependedOnBy":2,"privilegedJobs":0,"findings":0}],"total":1}
            """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/inventory?unit=workflows");
        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains("Dependents", page.Markup));
        Assert.Contains("Privileged Jobs", page.Markup);
        Assert.DoesNotContain("Privileged jobs", page.Markup);
        Assert.Contains(">2<", page.Markup);
    }

    // "Showing 0 of 0 matching" before an answer arrives is a measurement of nothing.
    [Fact]
    public void NothingIsCountedBeforeTheUnitHasAnswered()
    {
        var release = Withhold("api/jobs");
        Answer("api/jobs", """
            {"items":[{"nodeKey":"job:octo-org/app:ci.yml:build","jobId":"build",
              "workflow":"octo-org/app/.github/workflows/ci.yml","findings":2}],"total":1}
            """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?unit=jobs");
        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/jobs")));
        Assert.DoesNotContain("Showing 0", page.Markup);

        release();
        page.WaitForAssertion(() => Assert.Contains("Showing 1 of 1 matching", page.Markup));
    }

    /// <summary>
    /// The scope survives the switch, or a unit is a page wearing a different hat.
    /// </summary>
    [Fact]
    public void TheScopeSurvivesChangingUnit()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?properties=business_id%3A3");

        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() => Assert.Contains(
            "properties=business_id%3A3",
            Sent.Last(r => r.Url.Contains("api/repositories?")).Url));
    }

    /// <summary>
    /// The unit travels in the address, so a link hands over the same screen the sender was on.
    /// </summary>
    [Fact]
    public void TheUnitArrivedWithIsTheUnitShown()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?unit=repositories");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
            Assert.Contains(Sent, r => r.Url.Contains("api/repositories?")));
    }

    /// <summary>
    /// Every unit gets its own heading. A third unit added while the heading asked about two
    /// landed in the else branch, so a list of advisories sat under "Actions &amp; Workflows" and
    /// a paragraph about external dependencies. Every test was green; the browser read it in one
    /// glance, which is why this asserts the name of each unit rather than the shape of two.
    /// </summary>
    [Theory]
    [InlineData("dependencies", "Actions")]
    [InlineData("workflows", "Workflows")]
    [InlineData("jobs", "Jobs")]
    [InlineData("repositories", "Repositories")]
    [InlineData("advisories", "Advisories")]
    public void TheHeadingNamesTheUnitOnScreen(string unit, string heading)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
            Assert.Equal(heading, page.Find("h1,h2,h3,h4,h5,h6").TextContent.Trim()));
    }

    /// <summary>
    /// An entity written into a C# string is text, not markup: the heading rendered the five
    /// characters "&amp;amp;" on screen. Escaping is the renderer's job, and a literal entity
    /// anywhere in the page is the same bug wherever it appears.
    /// </summary>
    [Theory]
    [InlineData("dependencies")]
    [InlineData("repositories")]
    [InlineData("advisories")]
    public void NoEntityIsRenderedAsItsOwnText(string unit)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.DoesNotContain("&amp;amp;", page.Markup));
    }

    /// <summary>
    /// An advisory's rank is its place in the whole match, which the server knows and a page does
    /// not: the first row of a later page is not first.
    /// </summary>
    [Fact]
    public void TheAdvisoriesUnitShowsEachRowsRankInTheWholeMatch()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?unit=advisories");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Equal("4",
            page.Find("[data-abom-advisory-rank]").TextContent.Trim()));
    }

    /// <summary>
    /// The line under the grid claims an order. Naming the dependency default under a unit ordered
    /// by something else tells the reader the ranking is one thing while it is another — and the
    /// ranking is the only reason to read the top of a list at all.
    /// </summary>
    [Theory]
    [InlineData("dependencies", "reach")]
    [InlineData("repositories", "findings")]
    [InlineData("advisories", "severity, then reach")]
    public void TheOrderStatedIsTheOrderInForce(string unit, string order)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(
            $"ordered by {order} across everything that matched",
            System.Text.RegularExpressions.Regex.Replace(page.Markup, @"\s+", " ")));
    }

    /// <summary>
    /// The chosen unit has to be the most prominent of the three. Filled-on-Default renders as a
    /// dark fill with no border in the dark scheme, so the selected control read as the faintest
    /// thing on the row — technically distinct, and backwards to anyone looking at it.
    /// </summary>
    [Theory]
    [InlineData("dependencies")]
    [InlineData("repositories")]
    [InlineData("advisories")]
    public void TheChosenUnitIsTheProminentOne(string unit)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var chosen = page.Find($"[data-abom-unit='{unit}']");
            Assert.Contains("mud-button-filled-primary", chosen.ClassName);

            foreach (var other in page.FindAll("[data-abom-unit]")
                         .Where(b => b.GetAttribute("data-abom-unit") != unit))
            {
                Assert.DoesNotContain("mud-button-filled", other.ClassName);
            }
        });
    }

    /// <summary>
    /// The view in force is marked in the scope bar, for the same reason and in the same scheme.
    /// </summary>
    [Fact]
    public void TheSelectedViewIsMarked()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?scope=owned");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(
            "abom-view-selected", page.Find("[data-abom-view='owned']").ClassName));
    }

    /// <summary>
    /// The surface that owns the scope has to carry the control that builds it. Inventory applied
    /// a property scope out of the address and said nothing about it: the counts narrowed, the
    /// reader was given no way to see what narrowed them, and no way to narrow further without
    /// editing the URL. The Explorer had the control all along, which is how it went unnoticed.
    /// </summary>
    [Fact]
    public void TheSurfaceThatOwnsTheScopeCarriesTheControlThatBuildsIt()
    {
        Answer("api/properties", Properties);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?properties=business_id%3A3");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var chip = page.Find("[data-abom-filter-chip='properties']");
            Assert.Contains("business_id:3", chip.TextContent);
            Assert.NotNull(chip.QuerySelector("[data-abom-chip-remove]"));
        });
    }

    /// <summary>
    /// The heading describes what is listed. It read "Every external dependency the organization
    /// reaches" over a grid where, on the real corpus, 1,332 of 1,585 rows were repository-hosted
    /// workflows and composite actions, and 139 of those belonged to the organization itself. A
    /// reader who trusts the sentence mistakes their own workflows for somebody else's code.
    /// </summary>
    [Fact]
    public void TheDependenciesHeadingDoesNotCallOwnedRowsExternal()
    {
        Answer("api/actions", """
            {"items":[{"nodeKey":"octo-org/app/.github/workflows/ci.yml","kind":"workflow",
              "origin":"seed","dependentRepos":0,"dependentSteps":0,"distinctRefs":0,
              "findings":3,"advisories":0,"analyzed":true,"refs":[]}],"total":1}
            """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains("octo-org/app", page.Markup));
        Assert.DoesNotContain("Every external dependency", page.Markup);
    }
}
