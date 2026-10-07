using Abom.WebAssembly.Pages;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The catalog's unit is a query *version*, which no other surface has. What it must never do is
/// report a lifecycle decision nobody made, or attribute one to nobody.
/// </summary>
public class QueryCatalogTests : AbomTestContext
{
    private const string TwoVersions = """
        {"queries":[
          {"queryId":"abom/edges","queryVersion":"f4b6202355aa0011","pack":"advanced-security/abom-actions-graph-extraction-src",
           "packVersion":"2.27.0","cliVersion":"2.27.0","source":"abom","stage":"",
           "stagedBy":"","stagedAt":"","runs":9,"repositories":7,
           "firstRunAt":"2026-09-22T16:00:00Z","lastRunAt":"2026-09-22T16:20:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1aa22bb33cc4","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","source":"upstream","stage":"review",
           "stagedBy":"operator","stagedAt":"2026-09-22T15:00:00Z","runs":204,"repositories":204,
           "firstRunAt":"2026-09-20T10:00:00Z","lastRunAt":"2026-09-22T16:20:00Z"}
        ],"stages":["dev","test","extended-test","review","deploy"]}
        """;

    private const string SignedIn = """
        {"authenticated":true,"login":"operator","host":"github.com","accounts":[],"canSeeAllOrganizations":true}
        """;

    private const string SignedOut = """
        {"authenticated":false,"login":"","host":"","accounts":[],"canSeeAllOrganizations":false}
        """;

    private IRenderedComponent<CodeQL> Render(string catalog, string identity = SignedIn,
        string address = "/codeql?tab=catalog")
    {
        Answer("api/queries", catalog);
        Answer("api/accounts", identity);
        Fallback("api/runs", """{"runs":[]}""");
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo(address);
        var page = base.Render<CodeQL>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/queries")));
        return page;
    }

    /// <summary>
    /// A version nobody advanced has no stage. Rendering a default would report a decision the
    /// operator never made, which is the same failure as an unmeasured value printed as zero.
    /// </summary>
    [Fact]
    public void AVersionNobodyHasStagedSaysSoRatherThanShowingADefault()
    {
        var page = Render(TwoVersions);

        Assert.Contains("not staged", page.Markup);
        Assert.NotNull(page.Find("[data-abom-query-unstaged]"));
    }

    /// <summary>
    /// The stage a person did set is shown with who set it: an advance that could not name its
    /// author would be indistinguishable from one the tool awarded itself.
    /// </summary>
    [Fact]
    public void AStagedVersionNamesWhoAdvancedIt()
    {
        var page = Render(TwoVersions);

        var stage = page.Find("[data-abom-query-stage]");
        Assert.Contains("review", stage.TextContent);
        Assert.Contains("operator", stage.GetAttribute("title") ?? "");
    }

    /// <summary>
    /// Two versions of one query are two rows. Collapsing them would attach one readiness to two
    /// things that answer differently.
    /// </summary>
    [Fact]
    public void EveryQueryVersionIsItsOwnRow()
    {
        var page = Render(TwoVersions);

        Assert.Equal(2, page.FindAll("[data-abom-query-id]").Count);
    }

    /// <summary>
    /// The full hash stays on the element even though the cell abbreviates it, so a reader
    /// copying an identity gets the identity rather than the abbreviation.
    /// </summary>
    [Fact]
    public void TheAbbreviatedVersionStillCarriesTheWholeHash()
    {
        var page = Render(TwoVersions);

        var cell = page.Find("[data-abom-query-version]");
        Assert.Equal("f4b6202355aa0011", cell.GetAttribute("data-abom-query-version"));
        Assert.Equal("f4b6202355aa", cell.TextContent.Trim());
    }

    /// <summary>
    /// A query's source is read from the pack. `abom/` in a rule id is a naming convention anybody
    /// can adopt and is never evidence of where a query came from. The column says "Source", the
    /// word the filter language and the API use for it.
    /// </summary>
    [Fact]
    public void SourceIsRenderedFromThePackRatherThanTheRuleIdPrefix()
    {
        var page = Render(TwoVersions);

        var sources = page.FindAll("[data-abom-query-source]")
            .Select(e => e.GetAttribute("data-abom-query-source")).ToList();
        Assert.Contains("abom", sources);
        Assert.Contains("upstream", sources);
        var headers = page.FindAll("th").Select(h => h.TextContent.Trim()).ToList();
        Assert.Contains("Source", headers);
        Assert.DoesNotContain("Origin", headers);
    }

    /// <summary>
    /// A run recorded before versions were written is reported as unrecorded rather than dropped
    /// or shown as a version somebody chose.
    /// </summary>
    [Fact]
    public void ARunWithNoVersionSaysItWasNotRecorded()
    {
        var page = Render("""
            {"queries":[
              {"queryId":"abom/legacy","queryVersion":"","pack":"","packVersion":"","cliVersion":"",
               "source":"","stage":"","stagedBy":"","stagedAt":"","runs":2269,"repositories":1118,
               "firstRunAt":"2026-09-01T10:00:00Z","lastRunAt":"2026-09-20T10:00:00Z"}
            ],"stages":["dev","test"]}
            """);

        Assert.NotNull(page.Find("[data-abom-query-unversioned]"));
        Assert.DoesNotContain("data-abom-query-version=", page.Markup);
    }

    /// <summary>
    /// An empty catalog is a stage, not an apology: it says what has not happened and points at
    /// the thing that would change it, rather than rendering an empty table.
    /// </summary>
    [Fact]
    public void AnEmptyCatalogNamesWhatWouldFillIt()
    {
        var page = Render("""{"queries":[],"stages":["dev","test"]}""");

        var empty = page.Find("[data-abom-queries-empty]");
        Assert.Contains("No query has run yet", empty.TextContent);
        Assert.Contains("discovery", empty.TextContent, StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// Advancement is an act somebody signs. With no identity the page says why it cannot be
    /// signed instead of offering a control that would record a stage against nobody.
    /// </summary>
    [Fact]
    public void WithNoIdentityThePageSaysWhyItCannotAdvance()
    {
        var page = Render(TwoVersions, SignedOut);

        Assert.NotNull(page.Find("[data-abom-queries-unsigned]"));
    }

    /// <summary>With an identity, nothing warns and the control is offered.</summary>
    [Fact]
    public void WithAnIdentityAdvancingIsOffered()
    {
        var page = Render(TwoVersions);

        Assert.Empty(page.FindAll("[data-abom-queries-unsigned]"));
    }

    /// <summary>
    /// The counts sentence reports what is unstaged, because a catalog that only says how many
    /// versions exist leaves the reader to count the ones still needing a decision.
    /// </summary>
    [Fact]
    public void TheCountsSentenceSaysHowManyAreStillUnstaged()
    {
        var page = Render(TwoVersions);

        Assert.Contains("1 not yet staged",
            page.Find("[data-abom-queries-counts]").TextContent);
    }

    private const string WithDiscovery = """
        {"queries":[
          {"queryId":"abom/edges","queryVersion":"v1","pack":"advanced-security/abom-actions-graph-extraction-src",
           "packVersion":"2.27.0","cliVersion":"2.27.0","source":"abom","suite":"","kind":"",
           "kindRecorded":true,"discovery":true,"stage":"","stagedBy":"","stagedAt":"",
           "runs":2269,"repositories":1118,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","source":"upstream","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"stage":"","stagedBy":"","stagedAt":"",
           "runs":204,"repositories":204,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"}
        ],"stages":["dev","test"]}
        """;

    /// <summary>
    /// A query that builds the graph is not one somebody develops, and listing it beside the ones
    /// under review buries them under the crawl's own machinery.
    /// </summary>
    [Fact]
    public void ADiscoveryQueryIsNotListedByDefault()
    {
        var page = Render(WithDiscovery);

        var ids = page.FindAll("[data-abom-query-id]").Select(e => e.TextContent.Trim()).ToList();
        Assert.Contains("actions/unpinned-tag", ids);
        Assert.DoesNotContain("abom/edges", ids);
    }

    /// <summary>
    /// A default that hides things says so. Without the count the filtered catalog reads as the
    /// whole one, which asserts a smaller estate than exists.
    /// </summary>
    [Fact]
    public void TheHiddenDiscoveryQueriesAreCounted()
    {
        var page = Render(WithDiscovery);

        Assert.Contains("1 more", page.Find("[data-abom-queries-hidden]").TextContent);
    }

    /// <summary>Once shown, the crawl's queries are in the count, not added to it again.</summary>
    [Fact]
    public void ShowingTheCrawlsQueriesDoesNotCountThemTwice()
    {
        var page = Render(WithDiscovery);
        page.Find("[data-abom-queries-discovery-toggle] input").Change(true);

        var counts = page.Find("[data-abom-queries-counts]").TextContent;
        Assert.Contains("2 query versions", counts);
        Assert.DoesNotContain("more", counts);
        Assert.Contains("1 of them", counts);
    }

    private const string WithLane = """
        {"queries":[
          {"queryId":"abom-findings.qls","queryVersion":"","pack":"codeql/actions-queries",
           "packVersion":"0.6.34","cliVersion":"2.27.0","source":"upstream","suite":"abom-findings.qls",
           "kind":"","kindRecorded":false,"discovery":false,"lane":true,"stage":"","stagedBy":"","stagedAt":"",
           "runs":718,"repositories":363,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","source":"upstream","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"lane":false,"stage":"","stagedBy":"","stagedAt":"",
           "runs":204,"repositories":204,"analyses":2,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"}
        ],"stages":["dev","test"]}
        """;

    private const string OperatorWritten = """
        {"queries":[
          {"queryId":"acme/self-hosted-runner","queryVersion":"9b5327ab01d7","pack":"abom/custom-queries",
           "packVersion":"2.27.0","cliVersion":"2.27.0","source":"custom","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"lane":false,"stage":"","stagedBy":"","stagedAt":"",
           "tested":false,"passing":false,"testDetail":"","working":true,
           "steps":[
             {"step":"help","state":"pending","detail":"no src/SelfHostedRunner.md beside the query"},
             {"step":"expected","state":"passing","detail":"test/SelfHostedRunner/SelfHostedRunner.expected: 1 line"},
             {"step":"database","state":"pending","detail":"test/SelfHostedRunner has not been extracted"},
             {"step":"ast","state":"pending","detail":"no PrintAST run is recorded over the test database"},
             {"step":"compiles","state":"pending","detail":"not compiled for this version"},
             {"step":"tests","state":"pending","detail":"never run for this version"}],
           "runs":57,"repositories":57,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T17:40:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","source":"upstream","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"lane":false,"stage":"","stagedBy":"","stagedAt":"",
           "steps":[],
           "runs":204,"repositories":204,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"}
        ],"stages":["dev","test"]}
        """;

    /// <summary>
    /// Tests live beside the query, so only the working copy of a query the operator wrote has any
    /// to run. An upstream rule carries no Test action at all: its tests belong to the pack that
    /// ships it, and a button that can only fail is a claim about tests ABOM does not hold.
    /// </summary>
    [Fact]
    public void OnlyTheWorkingCopyOfTheOperatorsQueryOffersToRunItsTests()
    {
        var page = Render(OperatorWritten);

        var flask = Assert.Single(page.FindAll("[data-abom-query-test]"));
        Assert.False(flask.HasAttribute("disabled"), "the operator's own query");
        var row = flask.Closest("tr")!;
        Assert.Contains("acme/self-hosted-runner", row.TextContent);
    }

    /// <summary>
    /// The marks describe the version on disk, so they appear on that row and nowhere else, in the
    /// order served, with the flask standing for the tests step.
    /// </summary>
    [Fact]
    public void TheWorkingCopyCarriesItsMarksAndTheFlaskIsTheTestsMark()
    {
        var page = Render(OperatorWritten);

        var strip = Assert.Single(page.FindAll("[data-abom-steps]"));
        Assert.Contains("acme/self-hosted-runner", strip.Closest("tr")!.TextContent);
        var order = strip.QuerySelectorAll("[data-abom-step]").Select(e => e.GetAttribute("data-abom-step"));
        Assert.Equal(["help", "expected", "database", "ast", "compiles", "tests"], order);
        Assert.Equal("tests", page.Find("[data-abom-query-test]").GetAttribute("data-abom-step"));
    }

    /// <summary>
    /// A version no longer on disk cannot be tested, because the tests would run against what is
    /// there now and vouch for this one. What its tests last said is still shown.
    /// </summary>
    [Fact]
    public void AVersionNoLongerOnDiskShowsWhatItsTestsSaidAndCannotBeTested()
    {
        var page = Render("""
            {"queries":[
              {"queryId":"acme/self-hosted-runner","queryVersion":"03d18fc6adb0","pack":"abom/custom-queries",
               "source":"custom","kind":"problem","kindRecorded":true,"stage":"","working":false,"steps":[],
               "tested":true,"passing":true,"testDetail":"1 of 1 passed",
               "runs":1,"repositories":1,"lastRunAt":"2026-09-22T17:40:00Z"}
            ],"stages":["dev","test"]}
            """);

        Assert.Empty(page.FindAll("[data-abom-steps]"));
        var flask = page.Find("[data-abom-query-superseded-test]");
        Assert.True(flask.HasAttribute("disabled"));
        Assert.Contains("mud-success-text", flask.OuterHtml);
    }

    /// <summary>
    /// A query is written and tested before anything runs it. Its row says it has not run, rather
    /// than an empty date a reader would take for a missing value.
    /// </summary>
    [Fact]
    public void AQueryThatHasNotRunSaysSo()
    {
        var page = Render("""
            {"queries":[
              {"queryId":"acme/new","queryVersion":"aa11bb22cc33","pack":"abom/custom-queries",
               "source":"custom","kind":"problem","kindRecorded":true,"stage":"","working":true,
               "steps":[{"step":"tests","state":"pending","detail":"never run for this version"}],
               "runs":0,"repositories":0,"lastRunAt":""}
            ],"stages":["dev","test"]}
            """);

        Assert.Contains("not run yet", page.Find("[data-abom-query-never-run]").TextContent);
    }

    /// <summary>
    /// A version nobody has tested is not one whose tests failed, and only the second is a defect
    /// to go and look at. The control says which without a reader opening anything.
    /// </summary>
    [Fact]
    public void AnUntestedVersionDoesNotReadAsAFailingOne()
    {
        var page = Render(OperatorWritten);

        var label = page.FindAll("[data-abom-query-test]")
            .Select(b => b.GetAttribute("aria-label") ?? "")
            .First();

        Assert.NotNull(label);
        Assert.DoesNotContain("fail", page.Markup, StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// A suite the crawl invoked in one call is recorded as what ran, but it is not a query
    /// anybody writes or versions. Listing it puts a file name where a rule should be.
    /// </summary>
    [Fact]
    public void AWholeSuiteInvocationIsNotListedAsAQuery()
    {
        var page = Render(WithLane);

        var ids = page.FindAll("[data-abom-query-id]").Select(e => e.TextContent.Trim()).ToList();
        Assert.Contains("actions/unpinned-tag", ids);
        Assert.DoesNotContain("abom-findings.qls", ids);
    }

    /// <summary>What a version's runs found opens under that version's own row.</summary>
    [Fact]
    public void WhereAVersionRanOpensUnderItsOwnRow()
    {
        var page = Render(WithLane);

        Assert.Empty(page.FindAll("[data-abom-query-runs]"));
        page.Find("[data-abom-query-detail]").Click();

        page.WaitForAssertion(() =>
        {
            var runs = page.Find("[data-abom-query-runs]");
            Assert.NotNull(runs.Closest("[data-abom-queries-table]"));
        });
    }

    /// <summary>
    /// A request, a run or a link names a query, and following it opens that query's row rather
    /// than leaving a reader to find it in the list.
    /// </summary>
    [Fact]
    public void AnAddressNamingAQueryOpensItsRow()
    {
        var page = Render(OperatorWritten,
            address: "/codeql?tab=catalog&query=acme%2Fself-hosted-runner");

        page.WaitForAssertion(() =>
        {
            var runs = Assert.Single(page.FindAll("[data-abom-query-runs]"));
            Assert.Contains("acme/self-hosted-runner",
                runs.Closest("tr")!.PreviousElementSibling!.TextContent);
        });
    }

    /// <summary>A catalog row names the requests about its query, and each opens its own row.</summary>
    [Fact]
    public void ARowNamesTheRequestsAboutItsQuery()
    {
        var page = Render(OperatorWritten.Replace("\"working\":true,", "\"working\":true,\"requests\":[3,8],"),
            address: "/codeql?tab=catalog&query=acme%2Fself-hosted-runner");

        page.WaitForAssertion(() => Assert.Equal(2, page.FindAll("[data-abom-query-request]").Count));
        Assert.Equal(["/codeql?tab=requests&request=3", "/codeql?tab=requests&request=8"],
            page.FindAll("[data-abom-query-request]").Select(a => a.GetAttribute("href")));
    }

    /// <summary>
    /// A refusal somebody reads only after pressing is one they could have been told first, so
    /// extended-test is offered disabled beside what it would be refused with.
    /// </summary>
    [Fact]
    public void ExtendedTestSaysWhyItWouldBeRefusedBeforeItIsPressed()
    {
        var popover = base.Render<MudBlazor.MudPopoverProvider>();
        var page = Render(OperatorWritten
            .Replace("\"working\":true,", "\"working\":true,\"extendedTestRefusal\":\"its tests mark is pending: never run for this version\",")
            .Replace("\"stages\":[\"dev\",\"test\"]", "\"stages\":[\"dev\",\"test\",\"extended-test\"]"));

        page.FindAll("[data-abom-query-advance]")
            .Single(m => m.Closest("tr")!.TextContent.Contains("acme/self-hosted-runner"))
            .QuerySelector("button")!.Click();

        popover.WaitForAssertion(() => popover.Find("[data-abom-stage-item='extended-test']"));
        Assert.Contains("never run for this version",
            popover.Find("[data-abom-stage-refused]").TextContent);
        Assert.Equal("extended-test",
            popover.Find("[data-abom-stage-refused]").Closest("[data-abom-stage-item]")!.GetAttribute("data-abom-stage-item"));
        Assert.Single(popover.FindAll("[data-abom-stage-refused]"));
    }

    /// <summary>
    /// review is earned by a comparison with the alerts code scanning holds, and is offered disabled
    /// beside what it would be refused with, in the server's words.
    /// </summary>
    [Fact]
    public void ReviewSaysWhatWouldEarnItBeforeItIsPressed()
    {
        var popover = base.Render<MudBlazor.MudPopoverProvider>();
        var page = Render(OperatorWritten
            .Replace("\"working\":true,", "\"working\":true,\"reviewRefusal\":\"no run of this version has been compared with the alerts code scanning holds\",")
            .Replace("\"stages\":[\"dev\",\"test\"]", "\"stages\":[\"dev\",\"test\",\"review\"]"));

        page.FindAll("[data-abom-query-advance]")
            .Single(m => m.Closest("tr")!.TextContent.Contains("acme/self-hosted-runner"))
            .QuerySelector("button")!.Click();

        popover.WaitForAssertion(() => popover.Find("[data-abom-stage-item='review']"));
        var refused = Assert.Single(popover.FindAll("[data-abom-stage-refused]"));
        Assert.Contains("has been compared with the alerts", refused.TextContent);
        Assert.Equal("review", refused.Closest("[data-abom-stage-item]")!.GetAttribute("data-abom-stage-item"));
    }

    private const string OneRun = """
        {"runs":[{"rowId":1,"predicate":"repositories=octo%2Fapp,octo%2Fnone","mode":"regular",
          "requestedBy":"operator","state":"complete","createdAt":"2026-09-22T16:00:00Z",
          "finishedAt":"2026-09-22T16:04:00Z","results":4,
          "queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],
          "members":[],"tally":{"succeeded":1,"no_database":1}}]}
        """;

    private const string RunDetail = """
        {"rowId":1,"predicate":"repositories=octo%2Fapp,octo%2Fnone","mode":"regular",
         "requestedBy":"operator","state":"complete","createdAt":"2026-09-22T16:00:00Z",
         "finishedAt":"2026-09-22T16:04:00Z","results":0,
         "queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],
         "tally":{"succeeded":1,"no_database":1},
         "members":[
           {"repository":"octo/app","status":"succeeded","detail":"","databasePath":"/cache/app",
            "resultPath":"/work/run-1/octo_app.sarif","results":0,"counted":true,
            "byQuery":{"actions/unpinned-tag":0}},
           {"repository":"octo/none","status":"no_database","detail":"no analysis on record",
            "databasePath":"","resultPath":"","results":0,"counted":false,"byQuery":{}}]}
        """;

    /// <summary>
    /// A repository a query found nothing in and one it never looked at both show no findings.
    /// Printing a zero for the second reports a clean result for work that never happened.
    /// </summary>
    [Fact]
    public void ARepositoryNothingRanAgainstShowsNoCountRatherThanZero()
    {
        Answer("api/runs/1", RunDetail);
        Answer("api/runs", OneRun);
        var page = Render(WithLane);

        page.Find("[data-abom-query-detail]").Click();
        page.WaitForAssertion(() => page.Find("[data-abom-results-all] input"));
        page.Find("[data-abom-results-all] input").Change(true);

        page.WaitForAssertion(() =>
        {
            Assert.Equal("0", page.Find("[data-abom-member-results]").TextContent.Trim());
            Assert.NotNull(page.Find("[data-abom-member-uncounted]"));
        });
    }

    /// <summary>The suite's run figures, copied onto all 36 of its queries, described none.</summary>
    [Fact]
    public void NoColumnRepeatsTheSuitesFiguresAsAQuerys()
    {
        var page = Render(TwoVersions);

        var headers = page.FindAll("[data-abom-queries-table] thead th")
            .Select(h => h.TextContent.Trim()).ToList();
        Assert.DoesNotContain("Runs", headers);
        Assert.DoesNotContain("Repositories", headers);
        Assert.DoesNotContain("Analyses", headers);
    }

    /// <summary>"Yours" means owned; a query the operator wrote is custom.</summary>
    [Fact]
    public void AQueryTheOperatorWroteIsCustomRatherThanYours()
    {
        var page = Render(OperatorWritten);

        var sources = page.FindAll("[data-abom-query-source='custom']");
        Assert.Equal("custom", Assert.Single(sources).TextContent.Trim());
        Assert.DoesNotContain(">yours<", page.Markup);
    }

    /// <summary>
    /// Run on a row adds its version to the draft, here: the draft that holds several is one draft,
    /// on this page and the Inventory alike, rather than a copy on each.
    /// </summary>
    [Fact]
    public void RunningAVersionDraftsItOnThisPage()
    {
        var page = Render(OperatorWritten);
        var rows = page.FindAll("[data-abom-query-id]").Select(e => e.TextContent.Trim()).ToList();

        page.FindAll("[data-abom-query-run]")[rows.IndexOf("acme/self-hosted-runner")].Click();

        page.WaitForAssertion(() => page.Find("[data-abom-run-draft] [data-abom-run-picked='acme/self-hosted-runner']"));
        var at = Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri;
        Assert.EndsWith("/codeql?tab=catalog", at);
        var draft = Services.GetRequiredService<RunDraft>();
        Assert.Equal("9b5327ab01d7", Assert.Single(draft.Queries).QueryVersion);

        page.FindAll("[data-abom-query-run]")[rows.IndexOf("actions/unpinned-tag")].Click();
        page.WaitForAssertion(() => Assert.Equal(2, page.FindAll("[data-abom-run-picked]").Count));
    }

    /// <summary>Choosing repositories one by one is still the Inventory's, and the draft goes too.</summary>
    [Fact]
    public void RunningAVersionCanStillChooseItsRepositoriesInTheInventory()
    {
        var page = Render(OperatorWritten);
        var rows = page.FindAll("[data-abom-query-id]").Select(e => e.TextContent.Trim()).ToList();
        page.FindAll("[data-abom-query-run]")[rows.IndexOf("acme/self-hosted-runner")].Click();
        page.WaitForAssertion(() => page.Find("[data-abom-run-in-inventory]"));

        page.Find("[data-abom-run-in-inventory]").Click();

        var at = Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri;
        Assert.EndsWith("/inventory?unit=repositories", at);
        var draft = Services.GetRequiredService<RunDraft>();
        Assert.Equal("acme/self-hosted-runner", Assert.Single(draft.Queries).QueryId);
    }

    private const string RunViews = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true,
          "description":"Only what the repositories you declared hold.",
          "refuses":{"inventory":[],"repositories":[],"graph":[],"run":[]}},
         {"name":"unpinned","title":"Floating references","predicate":"ref=tag","builtin":true,
          "refuses":{"inventory":[],"repositories":["ref"],"graph":[],"run":["ref"]}},
         {"name":"mine","predicate":"q=octo&scope=owned","builtin":false,
          "refuses":{"inventory":[],"repositories":[],"graph":[],"run":[]}}]
        """;

    private const string RunTerms = """
        [{"name":"ref","label":"Reference style","help":"h","arity":"scalar","surfaces":["inventory"]},
         {"name":"scope","label":"Scope","help":"h","arity":"scalar","surfaces":["inventory"]}]
        """;

    private IRenderedComponent<CodeQL> Drafting()
    {
        Answer("api/views", RunViews);
        Answer("api/filters", RunTerms);
        Answer("api/databases", """
            {"databases":[
              {"repository":"octo-org/app","commit":"a1","sourceFilter":"f","mode":"regular",
               "status":"retained","path":"/db/app","bytes":10,"cliVersion":"2.27.1","reason":""}],
             "gaps":[{"repository":"octo-org/new","reason":"no regular analysis on record"}],
             "mode":"regular","unresolved":""}
            """);
        var page = Render(OperatorWritten);
        page.FindAll("[data-abom-query-run]")[0].Click();
        page.WaitForAssertion(() => page.Find("[data-abom-run-draft] [data-abom-view='owned']"));
        return page;
    }

    /// <summary>
    /// A run's scope here is a named one: those a run refuses are disabled with their terms named,
    /// and nothing is resolved until one is chosen.
    /// </summary>
    [Fact]
    public void ANamedScopeARunRefusesIsDisabledInTheDraftWithItsTermsNamed()
    {
        var page = Drafting();

        page.WaitForAssertion(() =>
            Assert.Contains("mud-disabled", page.Find("[data-abom-view='unpinned']").ClassName));
        var reason = page.Find("[data-abom-view-reason='unpinned']").GetAttribute("title") ?? "";
        Assert.Contains("A run cannot narrow by", reason);
        Assert.Contains("Reference style: tag", reason);
        Assert.DoesNotContain(Sent, r => r.Url.Contains("api/databases"));
        Assert.True(page.Find("[data-abom-run-go]").HasAttribute("disabled"));
    }

    /// <summary>
    /// A chosen scope shows its size before Run, from the resolution the run uses, and the run is
    /// asked for by the scope's name so the server records the name beside what it resolved to.
    /// </summary>
    [Fact]
    public void AChosenScopeShowsItsSizeAndTheRunIsAskedForByName()
    {
        var page = Drafting();
        Answer("api/runs", """{"rowId":9,"predicate":"scope=owned","scopeName":"owned","state":"pending"}""");
        Answer("api/runs/9/start", """{"rowId":9,"state":"running"}""");

        page.Find("[data-abom-view='owned']").Click();

        page.WaitForAssertion(() =>
            Assert.Contains("2 repositories", page.Find("[data-abom-run-size]").TextContent));
        Assert.Contains("What you own", page.Find("[data-abom-run-scope]").TextContent);
        Assert.Contains(Sent, r => r.Method == "GET" && r.Url.EndsWith("api/databases?view=owned"));

        page.Find("[data-abom-run-go]").Click();

        page.WaitForAssertion(() =>
            Assert.Contains(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/runs?view=owned")));
    }

    /// The draft names the scope it is over, and says what that scope holds on hover as the chip did.
    [Fact]
    public void TheDraftSaysWhatItsScopeHolds()
    {
        var page = Drafting();

        page.Find("[data-abom-view='owned']").Click();

        page.WaitForAssertion(() => Assert.Equal("Only what the repositories you declared hold.",
            page.Find("[data-abom-run-scope]").GetAttribute("title")));
    }

    /// <summary>
    /// Keep here names the scope the draft holds, which this page has none of until a named one is
    /// chosen, and it keeps that scope's predicate byte for byte.
    /// </summary>
    [Fact]
    public void KeepHereNamesTheChosenScopeByItsStoredPredicate()
    {
        // Left by another page: it is not what this draft is over.
        Services.GetRequiredService<ScopeService>().Set(ViewSpec.Parse("q=elsewhere"));
        var page = Drafting();

        Assert.True(page.Find("[data-abom-save-scope]").HasAttribute("disabled"),
            "Keep offered a scope this draft does not hold");

        page.Find("[data-abom-view='mine']").Click();
        page.Find("[data-abom-scope-name]").Input("mine again");
        page.Find("[data-abom-save-scope]").Click();

        page.WaitForAssertion(() =>
        {
            var kept = Sent.Last(r => r.Url.Contains("api/views/"));
            Assert.Equal("PUT", kept.Method);
            Assert.Contains("\"predicate\":\"q=octo\\u0026scope=owned\"", kept.Body);
        });
    }

    /// <summary>Two names both beginning "Run this version" sound like one control twice.</summary>
    [Fact]
    public void RunningAndTestingHaveNamesThatCannotBeConfused()
    {
        var page = Render(OperatorWritten);

        var run = page.Find("[data-abom-query-run]").GetAttribute("aria-label") ?? "";
        var test = page.Find("[data-abom-query-test]").GetAttribute("aria-label") ?? "";
        Assert.False(test.StartsWith(run, StringComparison.OrdinalIgnoreCase), $"{run} / {test}");
        Assert.False(run.StartsWith(test, StringComparison.OrdinalIgnoreCase), $"{run} / {test}");
    }

    /// <summary>
    /// Running a query and moving it through its lifecycle are different decisions, so they are
    /// different controls rather than two meanings of one dropdown.
    /// </summary>
    [Fact]
    public void RunningAndAdvancingAreSeparateActions()
    {
        var page = Render(TwoVersions);

        Assert.NotEmpty(page.FindAll("[data-abom-query-run]"));
        Assert.NotEmpty(page.FindAll("[data-abom-query-advance]"));
    }

    /// <summary>
    /// A discovery query reports no findings, so asking for one on demand answers nothing a
    /// reader is developing against. The control says why rather than failing when pressed.
    /// </summary>
    [Fact]
    public void RunningADiscoveryQueryIsRefusedWithAReason()
    {
        var page = Render(WithDiscovery);
        page.Find("[data-abom-queries-discovery-toggle] input").Change(true);

        var rows = page.FindAll("[data-abom-query-id]").Select(e => e.TextContent.Trim()).ToList();
        Assert.Contains("abom/edges", rows);

        var disabled = page.FindAll("[data-abom-query-run][disabled]");
        Assert.NotEmpty(disabled);
    }

    /// <summary>
    /// The pack and the suite are separate facts. One column holding both leaves a reader unable
    /// to tell a rule from the set it arrived in.
    /// </summary>
    [Fact]
    public void ThePackIsItsOwnColumnApartFromTheQuery()
    {
        var page = Render(TwoVersions);

        var packs = page.FindAll("[data-abom-query-pack]").ToList();
        Assert.Contains(packs, p => p.GetAttribute("data-abom-query-pack") == "codeql/actions-queries");
        // Every ABOM pack shares a scope and prefix, so only its own name tells two apart.
        Assert.Contains(packs, p => p.TextContent.Trim() == "abom-actions-graph-extraction-src");
    }

    /// <summary>The release rides on the pack as a pinned reference; its own column cost a column.</summary>
    [Fact]
    public void ThePackCarriesTheReleaseItCameIn()
    {
        var page = Render(TwoVersions);

        var cell = page.FindAll("[data-abom-query-pack]")
            .First(p => p.GetAttribute("data-abom-query-pack") == "codeql/actions-queries")
            .ParentElement!;
        Assert.Equal("actions-queries@0.6.35", cell.TextContent.Replace(" ", "").Trim());
        var headers = page.FindAll("[data-abom-queries-table] thead th").Select(h => h.TextContent.Trim());
        Assert.DoesNotContain("Pack version", headers);
    }
}
