using Abom.WebAssembly.Models;
using Abom.WebAssembly.Shared;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The strip is how a reader who does not read QL sees where a query stands, so every mark has to
/// say what it is and what backs it, and grey has to read as nothing yet rather than as failed.
/// </summary>
public class AuthoringStepsTests : AbomTestContext
{
    private static readonly AuthoringStep[] Seven =
    [
        new() { Step = "target", State = "passing", Detail = "acme/a, selected by you" },
        new() { Step = "help", State = "pending", Detail = "no src/A.md beside the query" },
        new() { Step = "expected", State = "warning", Detail = "test/A/A.expected is empty" },
        new() { Step = "database", State = "passing", Detail = "test/A/A.testproj, extracted" },
        new() { Step = "ast", State = "pending", Detail = "no PrintAST run is recorded" },
        new() { Step = "compiles", State = "failed", Detail = "A.ql:3:1: oops" },
        new() { Step = "tests", State = "failed", Detail = "0 of 1 passed" },
    ];

    private IRenderedComponent<AuthoringSteps> Strip(
        IReadOnlyList<AuthoringStep> steps, Action? onTest = null) =>
        Render<AuthoringSteps>(p =>
        {
            p.Add(s => s.Steps, steps);
            if (onTest is not null)
            {
                p.Add(s => s.OnTest, onTest);
                p.Add(s => s.CanTest, true);
            }
        });

    /// <summary>The strip is read left to right, so the order is part of what it says.</summary>
    [Fact]
    public void TheMarksKeepTheOrderTheyWereServedIn()
    {
        var strip = Strip(Seven);

        var order = strip.FindAll("[data-abom-step]").Select(e => e.GetAttribute("data-abom-step"));
        Assert.Equal(Seven.Select(s => s.Step), order);
    }

    /// <summary>
    /// A mark that is only a color is invisible to a screen reader and to anybody who cannot tell
    /// red from green, so the step, its state and its evidence are all in the name.
    /// </summary>
    [Fact]
    public void EveryMarkIsNamedWithItsStepStateAndEvidence()
    {
        var strip = Strip(Seven);

        var compiles = strip.Find("[data-abom-step=compiles]");
        Assert.Equal("img", compiles.GetAttribute("role"));
        var name = compiles.GetAttribute("aria-label") ?? "";
        Assert.Contains("Compiles", name);
        Assert.Contains("failed", name);
        Assert.Contains("A.ql:3:1", name);
    }

    /// <summary>Grey is nothing to show yet. A step nobody has taken is not a step that went wrong.</summary>
    [Fact]
    public void APendingMarkNeverReadsAsAFailure()
    {
        var strip = Strip(Seven);

        var help = strip.Find("[data-abom-step=help]");
        Assert.DoesNotContain("fail", help.GetAttribute("aria-label") ?? "",
            StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("mud-error-text", help.InnerHtml);
    }

    /// <summary>Each state has its own color, drawn from the palette rather than written here.</summary>
    [Fact]
    public void EachStateWearsItsOwnColor()
    {
        var strip = Strip(Seven);

        Assert.Contains("mud-success-text", strip.Find("[data-abom-step=target]").InnerHtml);
        Assert.Contains("mud-warning-text", strip.Find("[data-abom-step=expected]").InnerHtml);
        Assert.Contains("mud-error-text", strip.Find("[data-abom-step=compiles]").InnerHtml);
        var pending = strip.Find("[data-abom-step=help]").InnerHtml;
        Assert.DoesNotContain("mud-success-text", pending);
        Assert.DoesNotContain("mud-warning-text", pending);
    }

    [Fact]
    public void EveryMarkHasOneTooltip()
    {
        var strip = Strip(Seven);

        Assert.Equal(Seven.Length, strip.FindAll(".mud-tooltip-root").Count);
    }

    /// <summary>
    /// Where the strip can act, the tests mark is the Test action itself, in the tests' color, so
    /// the one control a reader presses is the one that shows what pressing it last said.
    /// </summary>
    [Fact]
    public void TheTestsMarkIsTheTestActionWhereOneIsGiven()
    {
        var pressed = 0;
        var strip = Strip(Seven[1..], () => pressed++);

        var flask = strip.Find("button[data-abom-query-test]");
        Assert.Equal("tests", flask.GetAttribute("data-abom-step"));
        Assert.Contains("mud-error-text", flask.OuterHtml);
        flask.Click();
        Assert.Equal(1, pressed);
    }

    [Fact]
    public void WithoutAnActionTheTestsMarkIsOnlyAMark()
    {
        var strip = Strip(Seven);

        Assert.Empty(strip.FindAll("button"));
        Assert.Equal("img", strip.Find("[data-abom-step=tests]").GetAttribute("role"));
    }
}
