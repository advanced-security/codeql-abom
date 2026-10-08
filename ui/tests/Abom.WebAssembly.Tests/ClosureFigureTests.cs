using System.Linq;
using System.Net;
using Abom.WebAssembly.Services;
using Abom.WebAssembly.Shared;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A details page draws what runs its node and what it runs, the same way every time: the server
/// draws it, and the page shows it, says what it holds when collapsed, and offers the file.
/// </summary>
public sealed class ClosureFigureTests : AbomTestContext
{
    private const string Figure = """
        {"key":"actions/checkout","svg":"<svg xmlns='http://www.w3.org/2000/svg' class='abom-fig' width='400' height='200'><a href='/node/octo/app/.github/workflows/ci.yml' aria-label='ci.yml'><rect/></a></svg>",
         "width":400,"height":200,
         "counts":{"drawn":25,"inClosure":186,"leftOut":161,"versions":9,"versionsDrawn":9,
                   "callers":120,"dependencies":0,"notListed":1057},
         "terms":["Scope: what you own"],
         "caption":["161 left out, yours and the nearest kept first; the Explorer draws all of it.","Terms: Scope: what you own.","Recorded 2026-10-06 13:30 UTC."],
         "direction":"LR","graphviz":"16.1.0"}
        """;

    private IRenderedComponent<ClosureFigure> RenderFigure(string predicate = "scope=owned") =>
        Render<ClosureFigure>(p =>
        {
            p.Add(c => c.Key, "actions/checkout");
            p.Add(c => c.Predicate, predicate);
        });

    /// Expanded on arrival, with the server's drawing inside and the page's terms in the request.
    [Fact]
    public void TheFigureIsDrawnOnArrival()
    {
        Answer("api/node/actions/checkout/-/closure", Figure);

        var cut = RenderFigure();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-figure-canvas] svg")));
        Assert.Equal("true", cut.Find("[data-abom-figure-toggle]").GetAttribute("aria-expanded"));
        var caption = cut.Find("[data-abom-figure-caption]");
        Assert.Contains("161 left out", caption.TextContent);
        Assert.Contains("Recorded 2026-10-06 13:30 UTC.", caption.TextContent);
        Assert.Empty(cut.FindAll("[data-abom-figure-canvas] [data-abom-figure-caption]"));
        Assert.DoesNotContain("Graphviz", cut.Find("[data-abom-figure]").TextContent);
        Assert.DoesNotContain("on the left", cut.Find("[data-abom-figure]").TextContent);
        var asked = Sent.Single(r => r.Url.Contains("/-/closure"));
        Assert.Contains("scope=owned", asked.Url);
    }

    /// <summary>
    /// Collapsed, it still says what it holds: one sentence, so a reader who closed it knows what
    /// they are not looking at.
    /// </summary>
    [Fact]
    public void ACollapsedFigureStillSaysWhatItHolds()
    {
        Answer("api/node/actions/checkout/-/closure", Figure);
        var cut = RenderFigure();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-figure-canvas]")));

        cut.Find("[data-abom-figure-toggle]").Click();

        Assert.Empty(cut.FindAll("[data-abom-figure-canvas]"));
        Assert.Empty(cut.FindAll("[data-abom-figure-rail]"));
        Assert.Equal("false", cut.Find("[data-abom-figure-toggle]").GetAttribute("aria-expanded"));
        var said = cut.Find("[data-abom-figure-summary]").TextContent;
        Assert.Contains("25 of 186 drawn", said);
        Assert.Contains("120 run it", said);
    }

    /// <summary>
    /// Beside the tables, a figure folds sideways into a rail that still says what it holds. Folded
    /// upward, it left its whole column standing empty beside them.
    /// </summary>
    [Fact]
    public void AFigureBesideTheTablesFoldsSidewaysIntoARail()
    {
        Answer("api/node/actions/checkout/-/closure", Figure);
        var cut = Render<ClosureFigure>(p =>
        {
            p.Add(c => c.Key, "actions/checkout");
            p.Add(c => c.Side, true);
        });
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-figure-canvas]")));

        cut.Find("[data-abom-figure-toggle]").Click();

        Assert.NotNull(cut.Find("[data-abom-figure-rail]"));
        Assert.Empty(cut.FindAll("[data-abom-figure-canvas]"));
        Assert.Equal("false", cut.Find("[data-abom-figure-toggle]").GetAttribute("aria-expanded"));
        Assert.Contains("25 of 186 drawn", cut.Find("[data-abom-figure-summary]").TextContent);

        cut.Find("[data-abom-figure-toggle]").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-figure-canvas]")));
        Assert.Empty(cut.FindAll("[data-abom-figure-rail]"));
    }

    /// The download is the same drawing as a file, asked for under the same terms.
    [Fact]
    public void TheDownloadIsTheSameDrawingUnderTheSameTerms()
    {
        Answer("api/node/actions/checkout/-/closure", Figure);
        var cut = RenderFigure();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-figure-download]")));
        var href = cut.Find("[data-abom-figure-download]").GetAttribute("href") ?? "";
        Assert.StartsWith("api/node/actions/checkout/-/closure.svg?", href);
        Assert.Contains("scope=owned", href);
        Assert.NotNull(cut.Find("[data-abom-figure-download]").GetAttribute("download"));
    }

    /// <summary>
    /// Folded, the rail says what the figure holds in text that reads across: each count under a
    /// word or two, and the whole sentence as the rail's name. Turned on its side, nobody read it.
    /// </summary>
    [Fact]
    public void AFoldedRailSaysWhatItHoldsInTextThatReadsAcross()
    {
        Answer("api/node/actions/checkout/-/closure", Figure);
        var cut = Render<ClosureFigure>(p =>
        {
            p.Add(c => c.Key, "actions/checkout");
            p.Add(c => c.Side, true);
            p.Add(c => c.Expanded, false);
        });

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-figure-stat]")));
        var stats = cut.FindAll("[data-abom-figure-stat]")
            .Select(s => (s.QuerySelector(".abom-figure-stat-label")?.TextContent.Trim(),
                s.QuerySelector(".abom-figure-stat-value")?.TextContent.Trim()))
            .ToList();
        Assert.Equal(
            [("Run by", "120"), ("Runs", "0"), ("Drawn", "25 of 186"), ("Versions", "9 of 9")],
            stats);
        Assert.Empty(cut.FindAll(".abom-figure-rail-text"));
        Assert.Contains("25 of 186 drawn", cut.Find("[data-abom-figure-rail] [data-abom-figure-summary]").TextContent);
    }

    /// A term the page cannot apply is the server's refusal, said in the figure's place.
    [Fact]
    public void ARefusedTermIsSaidWhereTheFigureWouldBe()
    {
        Refuse("/-/closure", HttpStatusCode.BadRequest, "kinds cannot narrow who uses a dependency");
        var cut = RenderFigure("kinds=action");

        cut.WaitForAssertion(() => Assert.Contains("kinds cannot narrow",
            cut.Find("[data-abom-figure-problem]").TextContent));
        Assert.Empty(cut.FindAll("[data-abom-figure-canvas]"));
    }

    /// A server that cannot run Graphviz is broken, and the figure says so with the remedy.
    [Fact]
    public void AServerWithoutGraphvizIsAFaultWithItsRemedy()
    {
        Refuse("/-/closure", HttpStatusCode.ServiceUnavailable,
            "this server cannot run Graphviz (layout engine is not installed: dot); install Graphviz so that dot is on the server's PATH");
        var cut = RenderFigure();

        cut.WaitForAssertion(() => Assert.Contains("install Graphviz",
            cut.Find("[data-abom-figure-problem]").TextContent));
        Assert.Equal("fault", cut.Find("[data-abom-figure-problem]").GetAttribute("data-abom-figure-problem"));
    }

    /// While it draws, the section holds its place so the tables under it do not jump.
    [Fact]
    public void ADrawingInProgressHoldsItsPlace()
    {
        var release = Withhold("/-/closure");
        Answer("api/node/actions/checkout/-/closure", Figure);

        var cut = RenderFigure();

        Assert.NotNull(cut.Find("[data-abom-figure-loading]"));
        release();
        cut.WaitForAssertion(() => Assert.Empty(cut.FindAll("[data-abom-figure-loading]")));
    }

    /// <summary>
    /// The drawing takes the colors a reader can change from the theme the page holds: a kind's
    /// color as the Explorer legend draws it, and each severity band. The rest the stylesheet maps.
    /// </summary>
    [Fact]
    public void TheFigureTakesTheColorsAReaderCanChange()
    {
        Answer("api/node/actions/checkout/-/closure", Figure);
        var cut = RenderFigure();

        var theme = Services.GetRequiredService<ThemeService>();
        var style = cut.Find("[data-abom-figure]").GetAttribute("style") ?? "";
        Assert.Contains($"--abom-fig-kind-action:{theme.Palette.Kinds["action"]}", style);
        Assert.Contains($"--abom-fig-medium:{theme.SeverityColor("medium")}", style);
        Assert.Contains($"--abom-fig-focus:{theme.Palette.Focus}", style);
        Assert.All(style.Split(';', StringSplitOptions.RemoveEmptyEntries), d => Assert.StartsWith("--", d.Trim()));
    }
}
