using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Who uses a node was computed and served before anything rendered it, so these pin the claims
/// the "What uses: this" table is allowed to make — including the ones it must refuse to make.
/// </summary>
public class NodeReachTests : AbomTestContext
{
    private const string Detail = """
        {"key":"actions/cache","kind":"action","repository":"","origin":"","path":"",
         "isExternal":true,"analyzed":true,"dependentRepos":2,
         "rules":[],"uses":[]}
        """;

    private static string Page(string items, int unanalyzed = 0) => $$"""
        {"key":"actions/cache","total":{{(items == "[]" ? 0 : items.Split("\"repository\"").Length - 1)}},
         "hiddenUpstream":0,"unanalyzed":{{unanalyzed}},"items":{{items}}}
        """;

    private void Serve(string uses)
    {
        Answer("/-/uses", uses);
        Answer("/-/versions", """{"key":"actions/cache","total":0,"items":[],"advisories":[]}""");
        Answer("api/node/", Detail);
    }

    private IRenderedComponent<Pages.NodePage> RenderNode() =>
        Render<Pages.NodePage>(p => p.Add(c => c.Key, "actions/cache"));

    private static void Open(IRenderedComponent<Pages.NodePage> cut, string repository) =>
        cut.Find($"[data-abom-use-repository='{repository}']").Closest("tr")!
            .QuerySelector("[data-abom-use-detail]")!.Click();

    // The route is the answer package-version matching cannot give. A count of affected
    // repositories without the path through them leaves a reader unable to tell whether the fix
    // is theirs to make.
    [Fact]
    public void TheRouteToTheNodeIsShown()
    {
        Serve(Page("""
            [{"repository":"octo-org/app","origin":"seed","reference":"v1","commit":"","hops":2,
              "through":"octo-org/lib/greet",
              "path":["octo-org/app/.github/workflows/ci.yml","octo-org/lib/greet","actions/cache"],
              "conditions":[],"workflows":[],"lines":0}]
            """));

        var cut = RenderNode();
        cut.WaitForAssertion(() => Open(cut, "octo-org/app"));

        cut.WaitForAssertion(() => Assert.Contains("octo-org/app/.github/workflows/ci.yml", cut.Markup));
        Assert.Contains("octo-org/lib/greet", cut.Find("[data-abom-use-route='octo-org/app']").TextContent);
    }

    // The condition is reported, never resolved. Printing a verdict here would be this tool
    // deciding something static analysis cannot.
    [Fact]
    public void AGuardedRouteShowsTheConditionVerbatim()
    {
        Serve(Page("""
            [{"repository":"octo-org/app","origin":"seed","reference":"v4","commit":"","hops":1,"through":"",
              "path":["octo-org/app/.github/workflows/ci.yml","actions/cache"],
              "conditions":[{"in":"octo-org/app/.github/workflows/ci.yml","condition":"runner.os == 'Linux'"}],
              "workflows":[],"lines":1}]
            """));

        var cut = RenderNode();
        cut.WaitForAssertion(() => Open(cut, "octo-org/app"));

        cut.WaitForAssertion(() => Assert.Contains("runner.os == 'Linux'", cut.Markup));
    }

    // An unguarded route is the stronger claim of the two, so it has to be stated rather than
    // left as an empty cell a reader has to interpret.
    [Fact]
    public void AnUnguardedRouteSaysSoRatherThanShowingNothing()
    {
        Serve(Page("""
            [{"repository":"octo-org/tool","origin":"seed","reference":"v4","commit":"","hops":1,"through":"",
              "path":["octo-org/tool/.github/workflows/ci.yml","actions/cache"],"conditions":[],
              "workflows":[],"lines":1}]
            """));

        var cut = RenderNode();
        cut.WaitForAssertion(() => Open(cut, "octo-org/tool"));

        cut.WaitForAssertion(() => Assert.Contains("unconditional", cut.Markup));
    }

    // Zero hops means the node lives in that repository. Labeling it "direct" alongside the
    // repositories that call it would say it depends on itself to run its own step.
    [Fact]
    public void ANodeInTheRepositoryItselfIsNotLabeledADirectDependency()
    {
        Serve(Page("""
            [{"repository":"octo-org/lib","origin":"seed","reference":"","commit":"","hops":0,"through":"",
              "path":["actions/cache"],"conditions":[],"workflows":[],"lines":0}]
            """));

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.Contains("lives here",
            cut.Find("[data-abom-use-relationship]").TextContent));
    }

    // Nothing using it is a fact about this record, not about the world: an unanalyzed dependency
    // could hold the route. Saying "none" without saying that reads as coverage.
    [Fact]
    public void NothingUsingItIsReportedWithTheCoverageCaveat()
    {
        Serve(Page("[]", unanalyzed: 4));

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.Contains("not the same as unused",
            cut.Find("[data-abom-uses-said]").TextContent));
        Assert.Contains("4 queued dependencies have not been", cut.Find("[data-abom-uses-said]").TextContent);
    }

    // The window where the answer is in flight is where a view can claim something it has not
    // been told. On a table whose subject is who runs this, that is the worst moment to guess.
    [Fact]
    public void NothingIsClaimedAboutUsesUntilTheAnswerArrives()
    {
        Serve(Page("[]"));
        var release = Withhold("/-/uses");

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-uses-table]")));
        Assert.DoesNotContain("not the same as unused", cut.Markup);

        release();
        cut.WaitForAssertion(() => Assert.Contains("not the same as unused", cut.Markup));
    }
}
