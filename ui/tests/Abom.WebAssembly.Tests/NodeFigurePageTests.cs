using System.Linq;
using Abom.WebAssembly.Models;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A details page draws what runs its node and what it runs, and the rest of the page answers for
/// the kind of node it is.
/// </summary>
public sealed class NodeFigurePageTests : AbomTestContext
{
    private const string Figure = """
        {"key":"k","svg":"<svg xmlns='http://www.w3.org/2000/svg' class='abom-fig' width='300' height='100'></svg>",
         "width":300,"height":100,
         "counts":{"drawn":3,"inClosure":3,"leftOut":0,"versions":1,"versionsDrawn":1,
                   "callers":0,"dependencies":2,"notListed":0},
         "terms":[],"direction":"LR","graphviz":"16.1.0"}
        """;

    private static string Detail(string key, string kind, string uses = "[]", string namedBy = "") => $$"""
        {"key":"{{key}}","kind":"{{kind}}","repository":"octo/app","origin":"seed","path":"",
         "isExternal":false,"analyzed":false,"dependentRepos":0,{{namedBy}}"rules":[],"uses":{{uses}}}
        """;

    private void Serve(string key, string kind, string uses = "[]", string usesPage = """{"key":"k","total":0,"items":[]}""",
        string namedBy = "")
    {
        Answer("/-/closure", Figure);
        Answer("/-/versions", """{"key":"k","total":0,"advisories":[],"items":[]}""");
        Answer("/-/uses", usesPage);
        Answer("api/node/", Detail(key, kind, uses, namedBy));
    }

    private IRenderedComponent<Pages.NodePage> RenderNode(string key) =>
        Render<Pages.NodePage>(p => p.Add(c => c.Key, key));

    /// The trail is the list that contains this kind. A step has no such list, so it has no middle crumb.
    [Theory]
    [InlineData("workflow", "workflows", "Workflows")]
    [InlineData("reusable_workflow", "workflows", "Workflows")]
    [InlineData("action", "dependencies", "Actions")]
    [InlineData("composite_action", "dependencies", "Actions")]
    [InlineData("docker_image", "dependencies", "Actions")]
    [InlineData("job", "jobs", "Jobs")]
    [InlineData("repo", "repositories", "Repositories")]
    [InlineData("step", "", "")]
    public void TheTrailNamesTheListThatContainsThisKind(string kind, string unit, string label)
    {
        Serve("octo/app/x", kind);
        var cut = RenderNode("octo/app/x");

        cut.WaitForAssertion(() => Assert.Equal("octo/app/x", cut.Find("h1").TextContent.Trim()));
        Assert.DoesNotContain("Back to Inventory", cut.Markup);
        if (unit.Length == 0)
        {
            Assert.Empty(cut.FindAll("[data-abom-node-unit]"));
            return;
        }
        var link = cut.Find("[data-abom-node-unit]");
        Assert.Equal(unit, link.GetAttribute("data-abom-node-unit"));
        Assert.Equal(label, link.TextContent.Trim());
        Assert.Equal($"/inventory?unit={unit}", link.GetAttribute("href"));
    }

    /// Every kind that runs something or is run draws a figure, above the tables by default.
    [Theory]
    [InlineData("workflow")]
    [InlineData("reusable_workflow")]
    [InlineData("action")]
    [InlineData("composite_action")]
    [InlineData("docker_image")]
    public void EveryKindThatRunsOrIsRunDrawsItsFigure(string kind)
    {
        Serve("octo/app/x", kind);
        var cut = RenderNode("octo/app/x");

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-node-layout='top'] [data-abom-figure]")));
    }

    /// The reader's placement is kept: a figure put beside the tables arrives there next time.
    [Fact]
    public void AFigurePutBesideTheTablesArrivesThere()
    {
        JSInterop.Setup<string?>("abomTheme.load", "abom.figurePlacement").SetResult("side");
        Serve("actions/checkout", "action");
        var cut = RenderNode("actions/checkout");

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-node-aside] [data-abom-figure]")));
        Assert.Equal("side", cut.Find("[data-abom-node-layout]").GetAttribute("data-abom-node-layout"));
    }

    /// <summary>
    /// With no choice made, a page wide enough to hold the tables and the figure side by side puts
    /// it beside them, where it stays in view; a narrower one puts it above.
    /// </summary>
    [Fact]
    public void AWidePageWithNoChoiceMadePutsTheFigureBesideTheTables()
    {
        JSInterop.Setup<BoxSize>("abomTheme.measure", "abom-node-layout").SetResult(new BoxSize(2100, 900));
        Serve("actions/checkout", "action");
        var cut = RenderNode("actions/checkout");

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-node-aside] [data-abom-figure]")));
    }

    /// Folded beside the tables, the figure gives them its column rather than standing empty.
    [Fact]
    public void AFigureFoldedBesideTheTablesGivesThemItsColumn()
    {
        JSInterop.Setup<string?>("abomTheme.load", "abom.figurePlacement").SetResult("side");
        Serve("actions/checkout", "action");
        var cut = RenderNode("actions/checkout");
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-node-aside] [data-abom-figure-canvas]")));

        cut.Find("[data-abom-figure-toggle]").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains("abom-node-layout-rail", cut.Find("[data-abom-node-layout]").ClassName));
        cut.Find("[data-abom-figure-toggle]").Click();
        cut.WaitForAssertion(() =>
            Assert.DoesNotContain("abom-node-layout-rail", cut.Find("[data-abom-node-layout]").ClassName));
    }

    /// Moving the figure is remembered for the next page.
    [Fact]
    public void MovingTheFigureIsRemembered()
    {
        Serve("actions/checkout", "action");
        var cut = RenderNode("actions/checkout");
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-figure-placement]")));

        cut.Find("[data-abom-figure-placement]").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-node-aside] [data-abom-figure]")));
        var saved = JSInterop.Invocations["abomTheme.save"].Last();
        Assert.Equal(["abom.figurePlacement", "side"], saved.Arguments.Select(a => a?.ToString()));
    }

    /// A repository runs nothing itself, so its page says where the figures are rather than drawing one.
    [Fact]
    public void ARepositoryPageSaysItsWorkflowsDrawTheirs()
    {
        Serve("repo:octo/app", "repo");
        var cut = RenderNode("repo:octo/app");

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-figure-repository]")));
        Assert.Empty(cut.FindAll("[data-abom-figure]"));
    }

    /// <summary>
    /// A workflow its triggers run has no versions and nothing names it in a uses: line. Its page
    /// said "No advisory on record names this action" and listed its own repository as "lives here"
    /// with 0 workflows.
    /// </summary>
    [Fact]
    public void ATriggeredWorkflowsPageListsNoVersionsAndNoUsers()
    {
        Serve("octo/app/.github/workflows/ci.yml", "workflow", namedBy: "\"namedBy\":0,");
        var cut = RenderNode("octo/app/.github/workflows/ci.yml");

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-figure]")));
        Assert.Empty(cut.FindAll("[data-abom-node-versions]"));
        Assert.Empty(cut.FindAll("[data-abom-node-uses]"));
        Assert.Contains("triggers", cut.Find("[data-abom-node-triggered]").TextContent);
    }

    /// <summary>
    /// A workflow another one calls by a local path keeps who calls it: 147 of the crawled
    /// organization's workflows are, and deciding by kind hid every one of them.
    /// </summary>
    [Fact]
    public void AWorkflowAnotherCallsKeepsWhoCallsIt()
    {
        Serve("octo/app/.github/workflows/release.yml", "workflow", namedBy: "\"namedBy\":1,");
        var cut = RenderNode("octo/app/.github/workflows/release.yml");

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-node-uses]")));
        Assert.Empty(cut.FindAll("[data-abom-node-triggered]"));
    }

    /// A server that does not say how many lines name it has not said "none".
    [Fact]
    public void AWorkflowWhoseCallersWereNotCountedKeepsTheTable()
    {
        Serve("octo/app/.github/workflows/ci.yml", "workflow");
        var cut = RenderNode("octo/app/.github/workflows/ci.yml");

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-node-uses]")));
        Assert.Empty(cut.FindAll("[data-abom-node-triggered]"));
    }

    [Fact]
    public void OneDependencyIsSaidInTheSingular()
    {
        Serve("octo/app/x", "composite_action", """[{"key":"actions/cache","kind":"action","refs":["v4"],"count":1}]""");
        var cut = RenderNode("octo/app/x");

        cut.WaitForAssertion(() => Assert.Contains("Uses 1 dependency", cut.Markup));
        Assert.DoesNotContain("Uses 1 dependencies", cut.Markup);
    }

    /// Two sentences under the uses table ran together: "…reference and commit.25 queued…".
    [Fact]
    public void TheSentencesUnderWhatUsesThisAreSpaced()
    {
        Serve("actions/cache", "action", usesPage: """
            {"key":"actions/cache","total":1,"hiddenUpstream":0,"unanalyzed":25,
             "items":[{"repository":"octo/app","origin":"seed","owned":true,"reference":"v4","commit":"",
                       "locked":false,"hops":1,"through":"","path":[],"conditions":[],"workflows":["octo/app/ci.yml"],"lines":1}]}
            """);
        var cut = RenderNode("actions/cache");

        cut.WaitForAssertion(() => Assert.Contains("25 queued", cut.Find("[data-abom-uses-said]").TextContent));
        var said = System.Text.RegularExpressions.Regex.Replace(cut.Find("[data-abom-uses-said]").TextContent, @"\s+", " ");
        Assert.DoesNotContain(".25 queued", said);
    }
}
