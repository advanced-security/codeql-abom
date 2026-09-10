using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Reach was computed and served for a release before anything rendered it, so these pin the
/// claims the panel is allowed to make — including the ones it must refuse to make.
/// </summary>
public class NodeReachTests : AbomTestContext
{
    private const string Detail = """
        {"key":"actions/cache","kind":"action","repository":"","origin":"","path":"",
         "isExternal":true,"analyzed":true,"dependentRepos":2,"directDependents":3,
         "refs":[],"rules":[],"advisories":[],"usedBy":[],"uses":[]}
        """;

    private void Serve(string reach)
    {
        Answer("/-/reach", reach);
        Answer("/-/impact", """{"key":"actions/cache","direct":1,"transitive":1,"repos":[],"unanalyzed":0}""");
        Answer("api/node/", Detail);
    }

    private IRenderedComponent<Pages.NodePage> RenderNode() =>
        Render<Pages.NodePage>(p => p.Add(c => c.Key, "actions/cache"));

    // The route is the answer package-version matching cannot give. A count of affected
    // repositories without the path through them leaves a reader unable to tell whether the fix
    // is theirs to make.
    [Fact]
    public void TheRouteToTheNodeIsShown()
    {
        Serve("""
            [{"repository":"octo-org/app","hops":2,
              "path":["octo-org/app/.github/workflows/ci.yml","octo-org/lib/greet","actions/cache"],
              "conditions":[]}]
            """);

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.Contains("octo-org/lib/greet", cut.Markup));
        Assert.Contains("octo-org/app/.github/workflows/ci.yml", cut.Markup);
    }

    // The condition is reported, never resolved. Printing a verdict here would be this tool
    // deciding something static analysis cannot.
    [Fact]
    public void AGuardedRouteShowsTheConditionVerbatim()
    {
        Serve("""
            [{"repository":"octo-org/app","hops":1,"path":["octo-org/app/.github/workflows/ci.yml","actions/cache"],
              "conditions":[{"in":"octo-org/app/.github/workflows/ci.yml","condition":"runner.os == 'Linux'"}]}]
            """);

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.Contains("runner.os == 'Linux'", cut.Markup));
    }

    // An unguarded route is the stronger claim of the two, so it has to be stated rather than
    // left as an empty cell a reader has to interpret.
    [Fact]
    public void AnUnguardedRouteSaysSoRatherThanShowingNothing()
    {
        Serve("""
            [{"repository":"octo-org/tool","hops":1,"path":["octo-org/tool/.github/workflows/ci.yml","actions/cache"],
              "conditions":[]}]
            """);

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.Contains("unconditional", cut.Markup));
    }

    // Zero hops means the step lives in that repository. Labeling it "direct" alongside the
    // repositories that call it would say it depends on itself to run its own step.
    [Fact]
    public void ANodeInTheRepositoryItselfIsNotLabeledADirectDependency()
    {
        Serve("""
            [{"repository":"octo-org/lib","hops":0,"path":["actions/cache"],"conditions":[]}]
            """);

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.Contains("lives here", cut.Markup));
    }

    // Nothing reaching it is a fact about this corpus, not about the world: an unanalyzed
    // dependency could hold the route. Saying "none" without saying that reads as coverage.
    [Fact]
    public void NothingReachingItIsReportedWithTheCoverageCaveat()
    {
        Serve("[]");

        var cut = RenderNode();

        cut.WaitForAssertion(() =>
            Assert.Contains("has not been analyzed", cut.Markup));
    }

    // The window where the answer is in flight is where a view can claim something it has not
    // been told. On a panel whose subject is what is and is not reachable, that is the worst
    // moment to guess.
    [Fact]
    public void NothingIsClaimedAboutReachUntilItsAnswerArrives()
    {
        Serve("[]");
        var release = Withhold("/-/reach");

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.Contains("What actually reaches this", cut.Markup));
        Assert.DoesNotContain("No repository here reaches this", cut.Markup);

        release();
        cut.WaitForAssertion(() =>
            Assert.Contains("No repository here reaches this", cut.Markup));
    }
}
