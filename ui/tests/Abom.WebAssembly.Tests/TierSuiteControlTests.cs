using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Choosing what a tier is analyzed with spends minutes of cloning and CodeQL for every
/// dependency in it, so the control has to say how much before it is pressed rather than after.
/// </summary>
public class TierSuiteControlTests : AbomTestContext
{
    private void Serve(string suites, int direct = 522, int transitive = 691)
    {
        Answer("api/discovery/suites", suites);
        Answer("api/discovery/status",
            DiscoveryStateBuilder.Drained().WithTiers(direct, transitive).ToJson());
        Answer("api/frontier", "[]");
    }

    private const string TwoSuites = """
        {"available":["ql/actions/abom/src/codeql-suites/abom-dependency-graph.qls",
                      "ql/actions/abom/src/codeql-suites/abom-findings.qls"],
         "tiers":{"direct":["ql/actions/abom/src/codeql-suites/abom-dependency-graph.qls"],
                  "transitive":[]},
         "counts":{"direct":{"total":522,"analyzed":522},"transitive":{"total":691,"analyzed":691}}}
        """;

    [Fact]
    public void EachTierStatesHowMuchWorkAChangeWouldApplyTo()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("What each tier is analyzed with", cut.Markup));
        Assert.Contains("522", cut.Markup);
        Assert.Contains("691", cut.Markup);
    }

    // A suite is offered by its own name, never by the tier it happens to default to. The offer
    // list is the control, so this looks inside the fold.
    [Fact]
    public void SuitesAreListedByTheirOwnName()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Discovery>();
        cut.WaitForAssertion(() => cut.Find(".abom-fold-toggle"));
        cut.Find(".abom-fold-toggle").Click();

        cut.WaitForAssertion(() => Assert.Contains("abom-dependency-graph", cut.Markup));
        Assert.Contains("abom-findings", cut.Markup);
    }

    // Binding nothing is an instruction, not a broken control, and the consequence is that the
    // tier is still fetched and simply never looked inside.
    [Fact]
    public void ATierBoundToNothingSaysWhatThatMeans()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() =>
            Assert.Contains("Nothing is bound to transitive", cut.Markup));
    }

    // "No suites on disk" and "no suites configured" are different facts, and a build whose
    // binding names a pack's suite is not broken.
    [Fact]
    public void NoSuitesOnDiskIsNotReportedAsNoneConfigured()
    {
        Serve("""{"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}""");

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() =>
            Assert.Contains("No suites were found on disk", cut.Markup));
        Assert.Contains("installed pack", cut.Markup);
    }

    // The window where the answer is in flight is where a view can claim a binding it has not
    // been told, which on this control would misstate what the crawl is about to run.
    [Fact]
    public void NothingIsClaimedAboutTheBindingUntilItArrives()
    {
        Serve(TwoSuites);
        var release = Withhold("api/discovery/suites");

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("What each tier is analyzed with", cut.Markup));
        Assert.DoesNotContain("Nothing is bound to", cut.Markup);
        Assert.DoesNotContain("No suites were found on disk", cut.Markup);

        release();
        cut.WaitForAssertion(() => Assert.Contains("Nothing is bound to transitive", cut.Markup));
    }

    // A read that failed is not a read still arriving. Leaving the in-flight indicator up made a
    // server too old to have the endpoint look like a view that was merely slow, indefinitely.
    [Fact]
    public void AFailedReadSaysSoRatherThanWaitingForEver()
    {
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().WithTiers(522, 691).ToJson());
        Answer("api/frontier", "[]");

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() =>
            Assert.Contains("Could not read what each tier is bound to", cut.Markup));
        Assert.DoesNotContain("mud-progress-linear", cut.Markup);
    }

    // Folding is what keeps the queue above the fold, but a fold that hides the binding turns
    // "direct runs findings" into something a reader has to go looking for.
    [Fact]
    public void TheBindingIsStatedEvenWhileTheControlIsFolded()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("What each tier is analyzed with", cut.Markup));
        Assert.Contains("direct: abom-dependency-graph", cut.Markup);
        Assert.Contains("transitive: nothing", cut.Markup);
        // The grid itself is genuinely absent rather than hidden, so ui-test can tell.
        Assert.DoesNotContain("abom-tier-head", cut.Markup);
    }

    // A section with no summary to show must not fold shut over the reason it has none.
    [Fact]
    public void AFailedReadIsNotFoldedAwayOverTheFailure()
    {
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().WithTiers(522, 691).ToJson());
        Answer("api/frontier", "[]");

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() =>
            Assert.Contains("Could not read what each tier is bound to", cut.Markup));
        Assert.DoesNotContain("abom-fold-toggle", cut.Markup);
    }

    // What the crawl is doing is why the page is open; only configuration folds.
    [Fact]
    public void TheRunControlsAreNeverFoldedAway()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Discovery>();

        // Asserted on the controls rather than their wording: the verb moved to a label shared by
        // the row when a third tier made repeating it wrap the last button onto its own line.
        cut.WaitForAssertion(() => Assert.Equal(2, cut.FindAll(".abom-requeue").Count));
        var requeue = cut.FindAll(".abom-requeue");
        Assert.Contains("direct", requeue[0].TextContent);
        Assert.Contains("transitive", requeue[1].TextContent);
        Assert.Contains("Re-analyze", cut.Markup);
    }

    private const string WithContents = """
        {"available":["advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"],
         "tiers":{"direct":["advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"],
                  "transitive":[]},
         "counts":{"direct":{"total":522,"analyzed":522},"transitive":{"total":691,"analyzed":691}},
         "contents":{"advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls":
           {"packs":[{"name":"codeql/actions-queries","version":"0.6.34",
                      "queries":["actions/unpinned-tag","actions/code-injection/medium"]}],
            "queryCount":2,"resolvedAt":"2026-09-05T14:00:00Z"}}}
        """;

    // A suite has no version of its own, so the line has to carry the pack's — which is the thing
    // that changes when the queries do.
    [Fact]
    public void ASuiteLineStatesItsPackVersionAndQueryCountWithoutExpanding()
    {
        Serve(WithContents);

        var cut = Render<Pages.Discovery>();
        cut.WaitForAssertion(() => cut.Find(".abom-fold-toggle"));
        cut.Find(".abom-fold-toggle").Click();

        cut.WaitForAssertion(() => Assert.Contains("abom-findings", cut.Markup));
        Assert.Contains("0.6.34", cut.Markup);
        Assert.Contains("2 queries", cut.Markup);
        // The query ids belong behind their own disclosure, not in the row.
        Assert.DoesNotContain("actions/unpinned-tag", cut.Markup);
    }

    // Auditability is the point: which rules ABOM will attribute has to be inspectable without
    // running the CLI.
    [Fact]
    public void ExpandingASuiteRevealsTheQueryIdsItResolvedTo()
    {
        Serve(WithContents);

        var cut = Render<Pages.Discovery>();
        cut.WaitForAssertion(() => cut.Find(".abom-fold-toggle"));
        cut.Find(".abom-fold-toggle").Click();
        cut.WaitForAssertion(() => cut.Find(".abom-suite-toggle"));
        cut.Find(".abom-suite-toggle").Click();

        cut.WaitForAssertion(() => Assert.Contains("actions/unpinned-tag", cut.Markup));
        Assert.Contains("actions/code-injection/medium", cut.Markup);
        Assert.Contains("codeql/actions-queries", cut.Markup);
    }

    // Never resolved is not resolved to nothing. Showing 0 would assert a measurement nobody took.
    [Fact]
    public void ASuiteNobodyHasResolvedSaysSoRatherThanShowingZero()
    {
        Serve(TwoSuites);

        var cut = Render<Pages.Discovery>();
        cut.WaitForAssertion(() => cut.Find(".abom-fold-toggle"));
        cut.Find(".abom-fold-toggle").Click();

        cut.WaitForAssertion(() => Assert.Contains("not yet resolved", cut.Markup));
        Assert.DoesNotContain("0 queries", cut.Markup);
    }
}
