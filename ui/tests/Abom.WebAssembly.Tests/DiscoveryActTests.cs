using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The crawl controls have to say which of the two acts each one performs.
///
/// <para>Enumeration lists the repositories under a declared account. Analysis builds a database
/// for each one and runs the suites. They cost wildly different things — seconds against minutes
/// per repository — and shipped, one button said "Start" and the other act had no presence at all,
/// so a first-run operator could not tell that anything had happened before they pressed it.</para>
/// </summary>
public sealed class DiscoveryActTests : AbomTestContext
{
    private void Serve(DiscoveryStateBuilder state, string ownedScope)
    {
        Answer("api/discovery/status", state.ToJson());
        Answer("api/frontier", "[]");
        Answer("api/owned-scope", ownedScope);
    }

    /// "Start" alone names no act. What it spends is minutes per repository, so it says so.
    [Fact]
    public void TheRunControlNamesAnalysisRatherThanSayingOnlyStart()
    {
        Serve(DiscoveryStateBuilder.NeverStarted().WithOwned(198, 0), "[{\"owner\":\"octo-org\"}]");

        var cut = Render<Shared.DiscoveryControls>();

        var run = cut.Find("[data-abom-run]");
        Assert.Contains("analysis", run.TextContent, StringComparison.OrdinalIgnoreCase);
    }

    /// With nothing declared there is nothing to analyze, and offering analysis as the next step
    /// sends the operator to a control that cannot help them.
    [Fact]
    public void NothingDeclaredSendsTheOperatorToSettingsRatherThanOfferingAnalysis()
    {
        Serve(DiscoveryStateBuilder.NeverStarted(), "[]");

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() =>
        {
            var act = cut.Find("[data-abom-enumeration]");
            Assert.Contains("Settings", act.TextContent);
        });
    }

    /// Enumeration's result is the owned tier, which is how it is observable at all — nothing
    /// records enumeration as an event. Reporting it apart from analysis is what makes the two
    /// acts distinguishable.
    [Fact]
    public void WhatEnumerationProducedIsStatedApartFromWhatAnalysisDid()
    {
        Serve(DiscoveryStateBuilder.NeverStarted().WithOwned(198, 12),
              "[{\"owner\":\"octo-org\"}]");

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() =>
        {
            var act = cut.Find("[data-abom-enumeration]");
            Assert.Contains("198", act.TextContent);
            Assert.DoesNotContain("Settings", act.TextContent);
        });
    }

    /// A declaration that has produced no repositories yet is a different state from one that
    /// produced some, and saying "0 repositories" as though that were a finding would be wrong.
    [Fact]
    public void ADeclarationThatHasEnumeratedNothingSaysSoRatherThanReportingZero()
    {
        Serve(DiscoveryStateBuilder.NeverStarted().WithOwned(0, 0), "[{\"owner\":\"octo-org\"}]");

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() =>
        {
            var act = cut.Find("[data-abom-enumeration]");
            Assert.Contains("octo-org", act.TextContent);
            Assert.DoesNotContain("0 repositories", act.TextContent);
        });
    }
}
