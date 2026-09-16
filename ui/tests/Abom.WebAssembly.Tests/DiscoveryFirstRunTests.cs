using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// What a database that has never been crawled says about itself. An empty closure and a
/// drained one produce the same zeroes, and reading the first as the second tells an operator
/// their coverage is complete before anything has been looked at.
/// </summary>
public class DiscoveryFirstRunTests : AbomTestContext
{
    // No depth has any references at all: nothing has been enumerated, let alone analyzed.
    private const string NothingEnumerated = """
        {"progress":[],"working":0,"hasOutstanding":false,"maxDepth":5,
         "recent":[],"totals":{"cycles":0,"millis":0,"fetches":0,"builds":0,
                               "evaluations":0,"probes":0,"analyzed":0,"discovered":0}}
        """;

    private void Serve(string objective, string ownedScope, string? prune = null)
    {
        Answer("api/discovery/objective", objective);
        Answer("api/discovery/status", DiscoveryStateBuilder.NeverStarted().ToJson());
        Answer("api/frontier", "[]");
        Answer("api/owned-scope", ownedScope);
        Answer("api/discovery/suites", """{"tiers":{},"available":[],"verified":false}""");
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", """{"verified":[],"problem":null}""");
        Answer("api/properties", """{"configured":"","keys":[],"breakdown":{"name":"","values":[]}}""");
        if (prune is not null)
        {
            Answer("api/prune", prune);
        }
    }

    /// <summary>
    /// An empty closure reports the same zero outstanding as a complete one. Claiming every
    /// reference has been analyzed is a coverage statement nothing has earned.
    /// </summary>
    [Fact]
    public void AClosureNothingHasEnteredIsNotReportedAsFullyAnalyzed()
    {
        Serve(NothingEnumerated, "[]");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("What the crawl is working on", cut.Markup));
        Assert.DoesNotContain("Every reference reached has been analyzed", cut.Markup);
        Assert.DoesNotContain("Nothing outstanding", cut.Markup);
    }

    /// <summary>
    /// The same panel must keep saying it for a closure that really did drain, or the fix has
    /// merely moved the lie.
    /// </summary>
    [Fact]
    public void AClosureThatDidDrainStillSaysSo()
    {
        Serve("""
            {"progress":[
               {"depth":1,"total":10,"done":10,"settled":10,"pending":0,"running":0,
                "available":0}],
             "working":0,"hasOutstanding":false,"maxDepth":5,
             "recent":[],"totals":{"cycles":3,"millis":900,"fetches":9,"builds":9,
                                   "evaluations":9,"probes":0,"analyzed":9,"discovered":0}}
            """, "[]");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("Every reference reached has been analyzed",
            cut.Markup));
    }

    /// <summary>
    /// The server refuses to prune against an empty closure, which is a state and not a fault.
    /// Reporting it as an unreachable server puts a raw transport error at the top of a first
    /// run, and hides the one banner that would mean something.
    /// </summary>
    [Fact]
    public void RefusingToPruneWithNothingDeclaredIsNotReportedAsAnUnreachableServer()
    {
        Serve(NothingEnumerated, "[]");
        Fail("api/prune", System.Net.HttpStatusCode.InternalServerError);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("What the crawl is working on", cut.Markup));
        Assert.DoesNotContain("could not reach the server", cut.Markup);
    }

    /// <summary>
    /// And the prune section says why it has nothing to show, rather than reading forever.
    /// </summary>
    [Fact]
    public void ThePruneSectionSaysItNeedsADeclarationRatherThanReadingForever()
    {
        Serve(NothingEnumerated, "[]");
        Fail("api/prune", System.Net.HttpStatusCode.InternalServerError);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("What the crawl is working on", cut.Markup));
        Assert.DoesNotContain("Reading what is outside your scope", cut.Markup);
    }

    /// <summary>
    /// A queue that has never held anything is not a drained one. Offering to re-check what was
    /// already analyzed names work that does not exist, and Start does nothing at all until
    /// something is declared.
    /// </summary>
    [Fact]
    public void AQueueThatNeverHeldAnythingDoesNotOfferToRecheckPriorWork()
    {
        Serve(NothingEnumerated, "[]");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("What the crawl is working on", cut.Markup));
        Assert.DoesNotContain("re-check what was already analyzed", cut.Markup);
    }

    /// <summary>
    /// Declared, but nothing enumerated under it yet — the state every new install passes through
    /// between declaring an account and the first enumeration finishing. This is what put
    /// "could not reach the server" at the top of a first run: the page guards on whether
    /// anything is declared, and the refusal is about whether anything is in the closure.
    /// </summary>
    [Fact]
    public void AClosureNothingHasBeenEnumeratedIntoSaysSoRatherThanFailing()
    {
        Serve(NothingEnumerated, """[{"owner":"has-ghas"}]""",
            """
            {"declared":0,"reachable":0,
             "removed":{"repositories":0,"nodes":0,"edges":0,"queued":0},
             "tombstoned":0,"applied":false,
             "unavailable":"Nothing has been enumerated yet, so there is no closure to be outside of."}
            """);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("What the crawl is working on", cut.Markup));
        Assert.DoesNotContain("could not reach the server", cut.Markup);
        Assert.Contains("Nothing has been enumerated yet", cut.Markup);
        // The trap a zero report sets: it renders as "nothing to remove", which on a corpus
        // nobody has measured is a reassurance rather than a missing answer.
        Assert.DoesNotContain("Nothing to remove", cut.Markup);
    }

    /// <summary>
    /// A genuine transport failure must still be reported. The suppression is for a refusal the
    /// page can explain, not for every error the prune endpoint might return.
    /// </summary>
    [Fact]
    public void ATransportFailureIsStillReportedWhenSomethingIsDeclared()
    {
        Serve(NothingEnumerated, """[{"owner":"octo-org"}]""");
        Fail("api/prune", System.Net.HttpStatusCode.InternalServerError);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("could not reach the server", cut.Markup));
    }
}
