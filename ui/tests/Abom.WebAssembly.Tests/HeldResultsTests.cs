using System.Net;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A run with findings off reads as what its queries reported, over the repositories it could
/// cover, with the ones it could not named first. None of it is a finding until somebody converts
/// it, all of it or a chosen part, and a false alarm is picked from it as from findings.
/// </summary>
public class HeldResultsTests : AbomTestContext
{
    private const string Held = """
        {"rowId":7,"predicate":"repositories=octo%2Fapp,octo%2Fquiet,octo%2Fnone,octo%2Fbroken",
         "mode":"regular","requestedBy":"operator","state":"complete","held":true,
         "createdAt":"2026-09-22T16:00:00Z","finishedAt":"2026-09-22T16:04:00Z","results":3,
         "queries":[{"queryId":"acme/rule","queryVersion":"v1"}],
         "tally":{"succeeded":2,"no_database":1,"failed":1},
         "members":[
           {"repository":"octo/app","status":"succeeded","counted":true,"results":3,"byQuery":{"acme/rule":3}},
           {"repository":"octo/quiet","status":"succeeded","counted":true,"results":0,"byQuery":{"acme/rule":0}},
           {"repository":"octo/none","status":"no_database","detail":"ABOM tracks no repository named octo/none",
            "counted":false,"results":0,"byQuery":{}},
           {"repository":"octo/broken","status":"failed","detail":"building its database: no workflows",
            "counted":false,"results":0,"byQuery":{}}]}
        """;

    private const string AppHeld = """
        {"repository":"octo/app","evaluated":true,"held":true,"truncated":false,"findings":[
          {"findingRowId":0,"heldResultId":41,"ruleId":"acme/rule","filePath":".github/workflows/ci.yml",
           "startLine":6,"startColumn":5,"message":"This job runs on a self-hosted runner.","severity":"warning"},
          {"findingRowId":900,"heldResultId":42,"convertedBy":"operator","convertedAt":"2026-09-22T17:00:00Z",
           "ruleId":"acme/rule","filePath":".github/workflows/release.yml","startLine":3,"startColumn":5,
           "message":"This job runs on a self-hosted runner.","severity":"warning"}]}
        """;

    public HeldResultsTests()
    {
        Answer("api/accounts", """{"authenticated":true,"login":"operator","accounts":[]}""");
    }

    private IRenderedComponent<Shared.RunResults> Opened()
    {
        Answer("api/runs/7/convert", """{"converted":1,"already":0}""");
        Answer("api/runs/7/results", AppHeld);
        Answer("api/runs/7", Held);
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 7L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));
        return cut;
    }

    /// <summary>A run over 2 of 4 repositories says something about 2, and says so before any count.</summary>
    [Fact]
    public void AHeldRunStatesItsCoverageFirstAndNamesWhatItCouldNotCover()
    {
        var cut = Opened();

        var coverage = cut.Find("[data-abom-held-coverage]");
        Assert.Contains("2 of 4 repositories", coverage.TextContent);
        Assert.Contains("octo/none", coverage.TextContent);
        Assert.Contains("octo/broken", coverage.TextContent);
        var summary = Words(cut.Find("[data-abom-run-summary]").TextContent);
        Assert.True(summary.IndexOf("2 of 4", StringComparison.Ordinal)
                    < summary.IndexOf("reported", StringComparison.Ordinal),
            "the coverage is not stated before the count");
        Assert.Contains("held until somebody converts them to findings", summary);
        Assert.Contains("see why. Its queries reported", summary);
    }

    private static string Words(string text) => System.Text.RegularExpressions.Regex.Replace(text, @"\s+", " ");

    /// <summary>Two sentences, not one word: Razor drops the space between an element and a code block.</summary>
    [Fact]
    public void ARunsSummaryKeepsItsSentencesApart()
    {
        Answer("api/runs/7", Held.Replace("\"held\":true", "\"held\":false"));
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 7L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));

        Assert.Contains("not. Its queries found", Words(cut.Find("[data-abom-run-summary]").TextContent));
    }

    /// <summary>What a member held is listed under it, each result convertible, and a result somebody
    /// already converted says so rather than offering itself again.</summary>
    [Fact]
    public void WhatARepositoryHeldIsListedWithEachResultConvertible()
    {
        var cut = Opened();

        cut.Find("[data-abom-held-toggle='octo/app']").Click();

        cut.WaitForAssertion(() =>
        {
            var listed = cut.Find("[data-abom-held='octo/app']");
            Assert.Contains(".github/workflows/ci.yml:6", listed.TextContent);
        });
        Assert.NotNull(cut.Find("[data-abom-held-result='41'] input"));
        Assert.Empty(cut.FindAll("[data-abom-held-result='42']"));
        Assert.Contains("converted by operator", cut.Find("[data-abom-held-result-converted='42']").TextContent);
        Assert.True(cut.Find("[data-abom-held-convert-chosen='octo/app']").HasAttribute("disabled"));
    }

    /// <summary>Converting the chosen results names them, says what it did, and reads the member again.</summary>
    [Fact]
    public void ConvertingChosenResultsSendsThemAndSaysWhatItDid()
    {
        var cut = Opened();
        cut.Find("[data-abom-held-toggle='octo/app']").Click();
        cut.WaitForAssertion(() => cut.Find("[data-abom-held-result='41'] input"));

        cut.Find("[data-abom-held-result='41'] input").Change(true);
        cut.Find("[data-abom-held-convert-chosen='octo/app']").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/runs/7/convert"));
            Assert.Contains("\"repository\":\"octo/app\"", sent.Body);
            Assert.Contains("\"results\":[41]", sent.Body);
            Assert.Contains("\"by\":\"operator\"", sent.Body);
            Assert.Contains("1 result converted to findings", cut.Find("[data-abom-held-converted]").TextContent);
        });
        Assert.Equal(2, Sent.Count(r => r.Url.Contains("api/runs/7/results")));
    }

    /// <summary>Converting the run sends no repository and no results: everything it held.</summary>
    [Fact]
    public void ConvertingAllSendsTheWholeRun()
    {
        var cut = Opened();

        cut.Find("[data-abom-held-convert-all]").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/runs/7/convert"));
            Assert.Contains("\"repository\":\"\"", sent.Body);
            Assert.Contains("\"results\":[]", sent.Body);
        });
    }

    /// <summary>A refusal is the server's reason, shown where the conversion was asked for.</summary>
    [Fact]
    public void ARefusedConversionSaysWhy()
    {
        Fail("api/runs/7/convert", HttpStatusCode.BadRequest);
        var cut = Opened();

        cut.Find("[data-abom-held-convert-all]").Click();

        cut.WaitForAssertion(() => Assert.Contains("refused", cut.Find("[data-abom-held-refused]").TextContent));
        Assert.Empty(cut.FindAll("[data-abom-held-converted]"));
    }

    /// <summary>Refining happens with findings off, so a false alarm is picked from what the run held.</summary>
    [Fact]
    public void AHeldResultIsPickedAsEvidence()
    {
        var cut = Opened();
        Assert.False(cut.Find("[data-abom-results-pick]").HasAttribute("disabled"));

        cut.Find("[data-abom-results-pick]").Click();
        cut.Find("[data-abom-member-findings='octo/app']").Click();
        cut.WaitForAssertion(() => cut.Find("[data-abom-pick-finding='41'] input"));
        Assert.Contains("held, not a finding", cut.Find("[data-abom-pick-held='41']").TextContent);

        cut.Find("[data-abom-pick-finding='41'] input").Change(true);
        cut.Find("[data-abom-results-ask]").Click();

        var draft = Services.GetRequiredService<RequestDraft>().Request;
        Assert.NotNull(draft);
        var picked = Assert.Single(draft!.Evidence);
        Assert.Equal(41, picked.HeldResultId);
        Assert.Equal(0, picked.FindingRowId);
        Assert.Equal("false-alarm", picked.Kind);
    }

    /// <summary>A run that stores findings keeps its own words and offers no conversion.</summary>
    [Fact]
    public void ARunThatStoresFindingsOffersNoConversion()
    {
        Answer("api/runs/7", Held.Replace("\"held\":true", "\"held\":false"));
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 7L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));

        Assert.Empty(cut.FindAll("[data-abom-held-coverage]"));
        Assert.Empty(cut.FindAll("[data-abom-held-toggle]"));
        Assert.Empty(cut.FindAll("[data-abom-held-convert-all]"));
        Assert.DoesNotContain("held until", cut.Find("[data-abom-run-summary]").TextContent);
    }

    private static string Listed(bool held) => $$$"""
        {"runs":[{"rowId":7,"predicate":"repositories=octo%2Fapp","mode":"regular","requestedBy":"operator",
          "state":"complete","held":{{{(held ? "true" : "false")}}},"createdAt":"2026-09-22T16:00:00Z",
          "finishedAt":"","results":3,"queries":[{"queryId":"acme/rule","queryVersion":"v1"}],
          "members":[],"tally":{"succeeded":1}}]}
        """;

    [Fact]
    public void AHeldRunIsMarkedWhereRunsAreListed()
    {
        Answer("api/runs", Listed(held: true));
        var cut = Render<Shared.AnalysisRuns>();
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-id]"));

        Assert.Equal("findings off", cut.Find("[data-abom-run-held]").TextContent.Trim());
        Assert.Equal("Run #7 again", cut.Find("[data-abom-run-again]").GetAttribute("aria-label"));
    }

    [Fact]
    public void ARunThatStoresFindingsIsNotMarked()
    {
        Answer("api/runs", Listed(held: false));
        var cut = Render<Shared.AnalysisRuns>();
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-id]"));

        Assert.Empty(cut.FindAll("[data-abom-run-held]"));
    }
}
