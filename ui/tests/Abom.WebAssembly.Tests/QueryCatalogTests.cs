using Abom.WebAssembly.Pages;
using Bunit;
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
           "packVersion":"2.27.0","cliVersion":"2.27.0","origin":"abom","stage":"",
           "stagedBy":"","stagedAt":"","runs":9,"repositories":7,
           "firstRunAt":"2026-09-22T16:00:00Z","lastRunAt":"2026-09-22T16:20:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1aa22bb33cc4","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","origin":"upstream","stage":"review",
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

    private IRenderedComponent<Queries> Render(string catalog, string identity = SignedIn)
    {
        Answer("api/queries", catalog);
        Answer("api/accounts", identity);
        return base.Render<Queries>();
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

        Assert.Equal("review", page.Find("[data-abom-query-stage]").TextContent.Trim());
        Assert.Contains("operator", page.Find("[data-abom-query-staged-by]").TextContent);
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
    /// Origin is read from the pack. `abom/` in a rule id is a naming convention anybody can
    /// adopt and is never evidence of where a query came from.
    /// </summary>
    [Fact]
    public void OriginIsRenderedFromThePackRatherThanTheRuleIdPrefix()
    {
        var page = Render(TwoVersions);

        var origins = page.FindAll("[data-abom-query-origin]")
            .Select(e => e.GetAttribute("data-abom-query-origin")).ToList();
        Assert.Contains("abom", origins);
        Assert.Contains("upstream", origins);
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
               "origin":"","stage":"","stagedBy":"","stagedAt":"","runs":2269,"repositories":1118,
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
           "packVersion":"2.27.0","cliVersion":"2.27.0","origin":"abom","suite":"","kind":"",
           "kindRecorded":true,"discovery":true,"stage":"","stagedBy":"","stagedAt":"",
           "runs":2269,"repositories":1118,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","origin":"upstream","suite":"","kind":"problem",
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

    private const string WithLane = """
        {"queries":[
          {"queryId":"abom-findings.qls","queryVersion":"","pack":"codeql/actions-queries",
           "packVersion":"0.6.34","cliVersion":"2.27.0","origin":"upstream","suite":"abom-findings.qls",
           "kind":"","kindRecorded":false,"discovery":false,"lane":true,"stage":"","stagedBy":"","stagedAt":"",
           "runs":718,"repositories":363,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","origin":"upstream","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"lane":false,"stage":"","stagedBy":"","stagedAt":"",
           "runs":204,"repositories":204,"analyses":2,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"}
        ],"stages":["dev","test"]}
        """;

    private const string OperatorWritten = """
        {"queries":[
          {"queryId":"acme/self-hosted-runner","queryVersion":"9b5327ab01d7","pack":"abom/custom-queries",
           "packVersion":"2.27.0","cliVersion":"2.27.0","origin":"user","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"lane":false,"stage":"","stagedBy":"","stagedAt":"",
           "tested":false,"passing":false,"testDetail":"",
           "runs":57,"repositories":57,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T17:40:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","origin":"upstream","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"lane":false,"stage":"","stagedBy":"","stagedAt":"",
           "runs":204,"repositories":204,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"}
        ],"stages":["dev","test"]}
        """;

    /// <summary>
    /// Tests live beside the query, so only a query the operator wrote has any to run. Offering
    /// the control on an upstream rule is a button that can only fail.
    /// </summary>
    [Fact]
    public void OnlyAQueryTheOperatorWroteOffersToRunItsTests()
    {
        var page = Render(OperatorWritten);

        var rows = page.FindAll("[data-abom-query-id]").Select(e => e.TextContent.Trim()).ToList();
        var buttons = page.FindAll("[data-abom-query-test]");

        Assert.Equal(rows.Count, buttons.Count);
        var mine = rows.IndexOf("acme/self-hosted-runner");
        var upstream = rows.IndexOf("actions/unpinned-tag");
        Assert.False(buttons[mine].HasAttribute("disabled"), "the operator's own query");
        Assert.True(buttons[upstream].HasAttribute("disabled"), "an upstream query");
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

    /// <summary>
    /// A run belongs to the version that was asked for. Sending a reader to a separate surface
    /// makes them carry the identity across and find it again among everyone else's runs.
    /// </summary>
    [Fact]
    public void WhatARunDidOpensOnTheVersionThatAskedForIt()
    {
        Answer("api/runs", """{"runs":[]}""");
        var page = Render(WithLane);

        var analyses = page.Find("[data-abom-query-analyses]");
        Assert.Null(analyses.GetAttribute("href"));

        analyses.Click();
        Assert.NotNull(page.Find("[data-abom-query-runs]"));
    }

    private const string OneRun = """
        {"runs":[{"rowId":1,"predicate":"rules=actions%2Funpinned-tag","mode":"regular",
          "requestedBy":"operator","state":"complete","createdAt":"2026-09-22T16:00:00Z",
          "finishedAt":"2026-09-22T16:04:00Z","results":4,
          "queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],
          "members":[],"tally":{"succeeded":1,"no_database":1}}]}
        """;

    private const string RunDetail = """
        {"rowId":1,"predicate":"rules=actions%2Funpinned-tag","mode":"regular",
         "requestedBy":"operator","state":"complete","createdAt":"2026-09-22T16:00:00Z",
         "finishedAt":"2026-09-22T16:04:00Z","results":0,
         "queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],
         "tally":{"succeeded":1,"no_database":1},
         "members":[
           {"repository":"octo/app","status":"succeeded","detail":"","databasePath":"/cache/app",
            "resultPath":"/work/run-1/octo_app.sarif","results":0,"counted":true},
           {"repository":"octo/none","status":"no_database","detail":"no analysis on record",
            "databasePath":"","resultPath":"","results":0,"counted":false}]}
        """;

    /// <summary>
    /// A repository a query found nothing in and one it never looked at both show no findings.
    /// Printing a zero for the second reports a clean result for work that never happened.
    /// </summary>
    [Fact]
    public void ARepositoryNothingRanAgainstShowsNoCountRatherThanZero()
    {
        Answer("api/runs", OneRun);
        Answer("api/runs/1", RunDetail);
        var page = Render(WithLane);

        page.Find("[data-abom-query-analyses]").Click();
        page.Find("[data-abom-run-inspect]").Click();

        Assert.Equal("0", page.Find("[data-abom-member-results]").TextContent.Trim());
        Assert.NotNull(page.Find("[data-abom-member-uncounted]"));
    }

    /// <summary>
    /// What a run found is the number two versions are compared by, so the list of runs carries it
    /// rather than making a reader open each one.
    /// </summary>
    [Fact]
    public void AListedRunSaysHowMuchItFound()
    {
        Answer("api/runs", OneRun);
        var page = Render(WithLane);

        page.Find("[data-abom-query-analyses]").Click();

        Assert.Equal("4", page.Find("[data-abom-run-results]").TextContent.Trim());
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

        var packs = page.FindAll("[data-abom-query-pack]").Select(e => e.TextContent.Trim()).ToList();
        Assert.Contains(packs, p => p.Contains("codeql/actions-queries"));
    }
}
