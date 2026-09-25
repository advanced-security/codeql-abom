using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A prediction reads as what its queries would report, over the repositories it could cover, with
/// the ones it could not named first. None of it is a finding, so none of it is evidence yet.
/// </summary>
public class PredictionResultsTests : AbomTestContext
{
    private const string Prediction = """
        {"rowId":7,"predicate":"repositories=octo%2Fapp,octo%2Fquiet,octo%2Fnone,octo%2Fbroken",
         "mode":"regular","requestedBy":"operator","state":"complete","prediction":true,
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

    private const string AppPredicted = """
        {"repository":"octo/app","evaluated":true,"predicted":true,"truncated":false,"findings":[
          {"findingRowId":0,"ruleId":"acme/rule","filePath":".github/workflows/ci.yml","startLine":6,
           "startColumn":5,"message":"This job runs on a self-hosted runner.","severity":"warning"}]}
        """;

    private IRenderedComponent<Shared.RunResults> Opened()
    {
        Answer("api/runs/7/results", AppPredicted);
        Answer("api/runs/7", Prediction);
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 7L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));
        return cut;
    }

    /// <summary>A prediction over 2 of 4 repositories is a prediction about 2, and says so before any count.</summary>
    [Fact]
    public void APredictionStatesItsCoverageFirstAndNamesWhatItCouldNotCover()
    {
        var cut = Opened();

        var coverage = cut.Find("[data-abom-prediction-coverage]");
        Assert.Contains("2 of 4 repositories", coverage.TextContent);
        Assert.Contains("octo/none", coverage.TextContent);
        Assert.Contains("octo/broken", coverage.TextContent);
        var summary = cut.Find("[data-abom-run-summary]").TextContent;
        Assert.True(summary.IndexOf("2 of 4", StringComparison.Ordinal)
                    < summary.IndexOf("would report", StringComparison.Ordinal),
            "the coverage is not stated before the count");
        Assert.Contains("stored as no finding", summary);
        Assert.Contains("see why. Its queries would report", Words(summary));
    }

    private static string Words(string text) => System.Text.RegularExpressions.Regex.Replace(text, @"\s+", " ");

    /// <summary>Two sentences, not one word: Razor drops the space between an element and a code block.</summary>
    [Fact]
    public void ARunsSummaryKeepsItsSentencesApart()
    {
        Answer("api/runs/7", Prediction.Replace("\"prediction\":true", "\"prediction\":false"));
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 7L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));

        Assert.Contains("not. Its queries found", Words(cut.Find("[data-abom-run-summary]").TextContent));
    }

    [Fact]
    public void APredictionOffersNoResultAsEvidenceAndSaysWhy()
    {
        var cut = Opened();

        Assert.True(cut.Find("[data-abom-results-pick]").HasAttribute("disabled"));
        Assert.Contains("prediction", cut.Find("[data-abom-results-pick-reason]").GetAttribute("title"));
    }

    [Fact]
    public void WhatARepositoryWouldReportIsListedUnderIt()
    {
        var cut = Opened();

        cut.Find("[data-abom-predicted-toggle='octo/app']").Click();

        cut.WaitForAssertion(() =>
        {
            var listed = cut.Find("[data-abom-predicted='octo/app']");
            Assert.Contains(".github/workflows/ci.yml:6", listed.TextContent);
            Assert.Contains("This job runs on a self-hosted runner.", listed.TextContent);
        });
        Assert.Empty(cut.FindAll("[data-abom-pick-finding]"));
    }

    /// <summary>A plain run keeps its own words: it found results, it did not predict them.</summary>
    [Fact]
    public void ARunThatIsNotAPredictionIsNotWordedAsOne()
    {
        Answer("api/runs/7", Prediction.Replace("\"prediction\":true", "\"prediction\":false"));
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 7L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));

        Assert.Empty(cut.FindAll("[data-abom-prediction-coverage]"));
        Assert.Empty(cut.FindAll("[data-abom-predicted-toggle]"));
        Assert.DoesNotContain("would report", cut.Find("[data-abom-run-summary]").TextContent);
        Assert.False(cut.Find("[data-abom-results-pick]").HasAttribute("disabled"));
    }

    private static string Listed(bool prediction) => $$$"""
        {"runs":[{"rowId":7,"predicate":"repositories=octo%2Fapp","mode":"regular","requestedBy":"operator",
          "state":"complete","prediction":{{{(prediction ? "true" : "false")}}},"createdAt":"2026-09-22T16:00:00Z",
          "finishedAt":"","results":3,"queries":[{"queryId":"acme/rule","queryVersion":"v1"}],
          "members":[],"tally":{"succeeded":1}}]}
        """;

    [Fact]
    public void APredictionIsMarkedWhereRunsAreListed()
    {
        Answer("api/runs", Listed(prediction: true));
        var cut = Render<Shared.AnalysisRuns>();
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-id]"));

        Assert.NotNull(cut.Find("[data-abom-run-prediction]"));
        Assert.Contains("again", cut.Find("[data-abom-run-again]").GetAttribute("aria-label"));
        Assert.Contains("Predict", cut.Find("[data-abom-run-again]").GetAttribute("aria-label"));
    }

    [Fact]
    public void ARunIsNotMarkedAsAPrediction()
    {
        Answer("api/runs", Listed(prediction: false));
        var cut = Render<Shared.AnalysisRuns>();
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-id]"));

        Assert.Empty(cut.FindAll("[data-abom-run-prediction]"));
    }
}
