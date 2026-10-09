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

    /// <summary>Grey is not yet. A step nobody has taken is not a step that went wrong.</summary>
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

        Assert.Contains("mud-success-text", strip.Find("[data-abom-step=database]").InnerHtml);
        Assert.Contains("mud-warning-text", strip.Find("[data-abom-step=expected]").InnerHtml);
        Assert.Contains("mud-error-text", strip.Find("[data-abom-step=compiles]").InnerHtml);
        var pending = strip.Find("[data-abom-step=help]").InnerHtml;
        Assert.DoesNotContain("mud-success-text", pending);
        Assert.DoesNotContain("mud-warning-text", pending);
    }

    /// <summary>
    /// Choosing a target is a decision, not a check the query passed, so it never wears the green a
    /// passing test does.
    /// </summary>
    [Fact]
    public void AChosenTargetIsNotGreen()
    {
        var target = Strip(Seven).Find("[data-abom-step=target]");

        Assert.DoesNotContain("mud-success-text", target.InnerHtml);
        Assert.Contains("mud-info-text", target.InnerHtml);
    }

    /// <summary>
    /// "Passing" said nothing about a target, a help file or a database; each step says what was
    /// done, and a warning says the step is unconfirmed rather than asking for "another look".
    /// </summary>
    [Fact]
    public void EachStepSaysWhatWasDoneRatherThanPassing()
    {
        var strip = Strip(Seven);
        string Name(string step) => strip.Find($"[data-abom-step={step}]").GetAttribute("aria-label") ?? "";

        Assert.StartsWith("Target: chosen", Name("target"));
        Assert.StartsWith("Test database: extracted", Name("database"));
        Assert.StartsWith("Expected results: unconfirmed", Name("expected"));
        Assert.StartsWith("Query help: not yet", Name("help"));
        Assert.All(strip.FindAll("[data-abom-step]"), mark =>
        {
            Assert.DoesNotContain("passing", mark.GetAttribute("aria-label") ?? "");
            Assert.DoesNotContain("another look", mark.GetAttribute("aria-label") ?? "");
        });
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

    /// <summary>
    /// A query nobody drove a session over earns its AST step when ABOM prints the tree, so where
    /// the strip can act the AST mark is that action, the way the tests mark is Test.
    /// </summary>
    [Fact]
    public void TheASTMarkPrintsTheTreeWhereTheStripCanAct()
    {
        var printed = 0;
        var strip = Render<AuthoringSteps>(p => p
            .Add(s => s.Steps, Seven[1..])
            .Add(s => s.OnPrint, () => printed++)
            .Add(s => s.CanPrint, true));

        var print = strip.Find("button[data-abom-query-print]");
        Assert.Equal("ast", print.GetAttribute("data-abom-step"));
        Assert.Contains("Print the tree", print.GetAttribute("aria-label") ?? "");
        print.Click();
        Assert.Equal(1, printed);
    }

    private IRenderedComponent<AuthoringSteps> Named(bool listed = false) =>
        Render<AuthoringSteps>(p => p
            .Add(s => s.Steps, Seven)
            .Add(s => s.QueryId, "acme/a")
            .Add(s => s.Listed, listed));

    /// <summary>
    /// A mark backed by a file opens it: the reader sees the .expected, the help, the query, the
    /// .actual or the printed tree rather than taking the color on trust.
    /// </summary>
    [Fact]
    public void AMarkBackedByAFileOpensIt()
    {
        Answer("api/queries/step", """
            {"step":"expected","files":[{"path":"test/A/A.expected","body":"| a job | flagged |","truncated":false}],"absent":""}
            """);
        var dialogs = Render<MudBlazor.MudDialogProvider>();
        var strip = Named();
        Assert.DoesNotContain("| a job | flagged |", dialogs.Markup);

        strip.Find("button[data-abom-step-view][data-abom-step=expected]").Click();

        dialogs.WaitForAssertion(() =>
        {
            Assert.Contains("| a job | flagged |", dialogs.Find("[data-abom-step-content=expected]").TextContent);
            Assert.Contains("test/A/A.expected", dialogs.Markup);
        });
        Assert.Contains(Sent, r => r.Url.Contains("api/queries/step?query=acme%2Fa&step=expected"));
    }

    [Fact]
    public void AStepWithNothingOnDiskSaysWhatIsMissing()
    {
        Answer("api/queries/step", """{"step":"help","files":[],"absent":"no src/A.md beside the query"}""");
        var dialogs = Render<MudBlazor.MudDialogProvider>();

        Named().Find("button[data-abom-step-view][data-abom-step=help]").Click();

        dialogs.WaitForAssertion(() =>
            Assert.Contains("no src/A.md beside the query", dialogs.Find("[data-abom-step-absent]").TextContent));
    }

    /// <summary>A target and a database are not files to read, so only the other five open.</summary>
    [Fact]
    public void OnlyTheStepsBackedByAFileCanBeOpened()
    {
        var strip = Named();

        var opens = strip.FindAll("button[data-abom-step-view]").Select(b => b.GetAttribute("data-abom-step"));
        Assert.Equal(["help", "expected", "ast", "compiles", "tests"], opens);
        Assert.Equal("img", strip.Find("[data-abom-step=target]").GetAttribute("role"));
        Assert.Equal(Seven.Length, strip.FindAll(".mud-tooltip-root").Count);
    }

    /// <summary>
    /// Listed, each step is the same mark with its words beside it, so it has the strip's tooltip
    /// and opens the same file.
    /// </summary>
    [Fact]
    public void AListedStepIsTheSameMarkWithItsWords()
    {
        var list = Named(listed: true);

        var lines = list.FindAll("[data-abom-request-step]");
        Assert.Equal(Seven.Length, lines.Count);
        Assert.All(lines, line => Assert.Single(line.QuerySelectorAll(".mud-tooltip-root")));
        Assert.Contains("no src/A.md beside the query", list.Find("[data-abom-request-step=help]").TextContent);
        Assert.NotNull(list.Find("[data-abom-request-step=expected] button[data-abom-step-view]"));
    }
}
