using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The owned tier held only dependencies until repositories were enumerated into the queue. It
/// then reported one number for two things: on the reference corpus, 198 repositories of which 91
/// were analyzed, and 44 dependencies of which <b>none</b> were — shown as "242 dependencies, 91
/// analyzed", which says the operator's supply chain is a third covered when no part of it is.
/// </summary>
public class OwnedTierSplitTests : AbomTestContext
{
    // `verified` is what makes the owned tier appear at all: without an identity, an owned count
    // of zero would be a claim rather than a measurement.
    private const string Suites = """
        {"available":["ql/actions/abom/src/codeql-suites/abom-findings.qls"],
         "tiers":{"owned":["ql/actions/abom/src/codeql-suites/abom-findings.qls"],
                  "direct":["ql/actions/abom/src/codeql-suites/abom-findings.qls"],
                  "transitive":[]},
         "verified":true}
        """;

    private void Serve(int repositories, int repositoriesAnalyzed, int total, int analyzed)
    {
        Answer("api/discovery/suites", Suites);
        Answer("api/discovery/objective", """{"depths":[]}""");
        Answer("api/discovery/status",
            "{\"available\":true," +
            "\"status\":{\"state\":\"paused\",\"concurrency\":4,\"maxDepth\":5}," +
            "\"frontier\":{\"done\":0,\"queued\":0,\"running\":0,\"unavailable\":0}," +
            "\"tiers\":{" +
            Tier("owned", total, analyzed, repositories, repositoriesAnalyzed) + "," +
            Tier("direct", 10, 5, 0, 0) + "," +
            Tier("transitive", 20, 9, 0, 0) + "}," +
            "\"version\":{\"reachRecomputedAt\":\"\"},\"advisoriesFetchedAt\":\"\"}");
        Answer("api/frontier", "[]");
    }

    private static string Tier(string name, int total, int analyzed, int repos, int reposAnalyzed) =>
        $"\"{name}\":{{\"total\":{total},\"analyzed\":{analyzed}," +
        $"\"repositories\":{repos},\"repositoriesAnalyzed\":{reposAnalyzed}}}";

    // The per-tier counts head the suite grid on the CodeQL page's Suites part.
    private static IRenderedComponent<Shared.TierSuites> Opened(AbomTestContext ctx) =>
        ctx.Render<Shared.TierSuites>();

    // The two halves answer different questions — "have my repositories been read" and "has my
    // supply chain been read" — and one number cannot say that none of the second has.
    [Fact]
    public void TheOwnedTierNamesRepositoriesAndDependenciesSeparately()
    {
        Serve(repositories: 198, repositoriesAnalyzed: 91, total: 242, analyzed: 91);

        var cut = Opened(this);

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-tier-split='owned']")));
        var text = cut.Find("[data-abom-tier-split='owned']").TextContent;
        Assert.Contains("198", text);
        Assert.Contains("44", text);
    }

    // Calling a repository a dependency is the specific falsehood: 198 of the 242 are the
    // operator's own code, not something they depend on.
    [Fact]
    public void RepositoriesAreNotCalledDependencies()
    {
        Serve(repositories: 198, repositoriesAnalyzed: 91, total: 242, analyzed: 91);

        var cut = Opened(this);

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-tier-split='owned']")));
        var text = cut.Find("[data-abom-tier-split='owned']").TextContent;
        Assert.Contains("repositor", text);
    }

    // A tier holding no repositories is every tier but owned, and a split there would invent a
    // distinction that does not apply.
    [Fact]
    public void ATierWithNoRepositoriesShowsNoSplit()
    {
        Serve(repositories: 198, repositoriesAnalyzed: 91, total: 242, analyzed: 91);

        var cut = Opened(this);

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-tier-split='owned']")));
        Assert.Empty(cut.FindAll("[data-abom-tier-split='direct']"));
    }

    // Before enumeration the owned tier is dependencies only, exactly as it always was, and a
    // split reading "0 repositories" would imply the operator owns none.
    [Fact]
    public void WithNoRepositoriesQueuedTheOwnedTierReadsAsItAlwaysDid()
    {
        Serve(repositories: 0, repositoriesAnalyzed: 0, total: 44, analyzed: 3);

        var cut = Opened(this);

        cut.WaitForAssertion(() => Assert.Contains("owned", cut.Markup));
        Assert.Empty(cut.FindAll("[data-abom-tier-split='owned']"));
    }
}
