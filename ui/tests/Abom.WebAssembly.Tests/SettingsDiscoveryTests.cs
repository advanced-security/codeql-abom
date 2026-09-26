using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Settings is where the crawl is started and where what it runs is decided.
///
/// <para>The four-surfaces refactoring took Discovery out of the navigation on the grounds that it
/// was "how the Dashboard is configured" — and then the Dashboard became a launcher and absorbed
/// none of it. The page went on working at its old address while the only way in was one button
/// labelled like a report, so an operator looking for the tier-to-suite binding could not find it.
/// These assert the way in exists, not merely that the page does.</para>
/// </summary>
public sealed class SettingsDiscoveryTests : AbomTestContext
{
    private void Serve(string suites)
    {
        Answer("api/discovery/status", DiscoveryStateBuilder.NeverStarted().WithOwned(25, 17).ToJson());
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", "{\"verified\":[],\"problem\":null}");
        Answer("api/discovery/suites", suites);
    }

    private const string TwoSuites = """
        {
          "verified": false,
          "available": ["codeql/abom-findings.qls", "codeql/abom-dependency-graph.qls"],
          "tiers": { "direct": ["codeql/abom-findings.qls"], "transitive": [] },
          "contents": {}
        }
        """;

    /// Analysis starts here. Without this control an operator has a declaration and no way to act
    /// on it from the surface that took the declaration.
    [Fact]
    public void AnalysisCanBeStartedFromSettings()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() =>
        {
            var run = cut.Find("[data-abom-run]");
            Assert.Contains("analysis", run.TextContent, StringComparison.OrdinalIgnoreCase);
        });
    }

    /// Settings states which suite each tier runs, and names where that is changed.
    [Fact]
    public void WhatEachTierIsAnalyzedWithIsStatedInSettingsAndChangedOnTheCodeQLPage()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() =>
        {
            Assert.Contains(
                "What each tier is analyzed with",
                cut.Markup,
                StringComparison.Ordinal);
            Assert.Equal("/codeql?tab=suites",
                cut.Find("[data-abom-binding-change]").GetAttribute("href"));
        });
    }

    /// Stated only where it is changed, a binding leaves a reader on Settings assuming the default.
    [Fact]
    public void TheBindingStatesItselfWithoutBeingOpened()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() =>
        {
            var summary = cut.Find("[data-abom-binding-summary]");
            Assert.Contains("direct:", summary.TextContent, StringComparison.Ordinal);
            Assert.Contains("abom-findings", summary.TextContent, StringComparison.Ordinal);
        });
    }

    /// An empty binding is a legitimate instruction, so it is stated rather than corrected. Read
    /// as "the default is running", it is the opposite of what the crawl will do.
    [Fact]
    public void ATierBoundToNothingSaysSoRatherThanLookingUnset()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() =>
            Assert.Contains("Nothing is bound to transitive", cut.Markup, StringComparison.Ordinal));
    }

    /// Re-analysis is the other half of initiating: it spends minutes per repository, so what it
    /// applies to is on the control rather than behind it.
    [Fact]
    public void ReAnalyzingATierIsOfferedWithWhatItAppliesTo()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() =>
        {
            var tiers = cut.FindAll(".abom-requeue-tier");
            Assert.NotEmpty(tiers);
            Assert.Contains(tiers, t => t.TextContent.Contains("direct", StringComparison.Ordinal));
        });
    }
}
