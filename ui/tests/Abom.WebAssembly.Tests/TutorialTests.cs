using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class TutorialTests : AbomTestContext
{
    [Fact]
    public void HomeOffersSixRepeatableLessonsWithoutReplacingItsScopeEntries()
    {
        Answer("api/summary", "{}");
        var cut = Render<Pages.Home>();

        Assert.Equal(6, cut.FindAll("[data-abom-tutorial-lesson]").Count);
        Assert.Equal(6, cut.FindAll("[data-abom-tutorial-start]").Count);
        Assert.Equal(6, cut.FindAll("[data-abom-entry]").Count);
        var tutorials = cut.Find("details[data-abom-tutorial-checklist]");
        Assert.Contains("ABOM Tutorials", tutorials.QuerySelector("summary")!.TextContent);
        Assert.True(tutorials.HasAttribute("open"));
        Assert.DoesNotContain("Walkthrough completion", cut.Markup);
        Assert.True(cut.Markup.IndexOf("data-abom-tutorial-checklist", StringComparison.Ordinal) <
                    cut.Markup.IndexOf("data-abom-entry", StringComparison.Ordinal));
    }

    [Fact]
    public async Task TutorialTitlesAndPlayIconsStartTheSameLessonWithDistinctTooltips()
    {
        var tutorial = Services.GetRequiredService<TutorialService>();
        await tutorial.InitializeAsync();
        var cut = Render<Shared.TutorialChecklist>();
        Assert.All(cut.FindComponents<MudBlazor.MudIconButton>(),
            icon => Assert.Equal(MudBlazor.Icons.Material.Outlined.PlayArrow, icon.Instance.Icon));
        foreach (var lesson in TutorialLessons.All)
        {
            var title = cut.Find($"[data-abom-tutorial-title='{lesson.Id}']");
            Assert.Equal(lesson.Title, title.TextContent.Trim());
            Assert.Equal(lesson.Description, title.Closest("[data-abom-tip]")!.GetAttribute("data-abom-tip"));
            var icon = cut.Find($"[data-abom-tutorial-start='{lesson.Id}']");
            Assert.Empty(icon.TextContent.Trim());
            Assert.Equal("Start this tutorial.", icon.Closest("[data-abom-tip]")!.GetAttribute("data-abom-tip"));
        }
        Assert.Contains("Request a new detection rule", cut.Markup);
        cut.Find("[data-abom-tutorial-title='new-rule']").Click();
        Assert.Equal("new-rule", tutorial.Active?.Id);
        await cut.InvokeAsync(() => tutorial.ExitAsync());
        cut.Find("[data-abom-tutorial-start='lifecycle']").Click();
        Assert.Equal("lifecycle", tutorial.Active?.Id);
    }

    [Fact]
    public async Task ACompletedLessonOffersARestartIconRatherThanRepeatText()
    {
        var tutorial = Services.GetRequiredService<TutorialService>();
        await tutorial.InitializeAsync();
        await tutorial.StartAsync("orientation");
        while (!tutorial.LastStep) tutorial.Next();
        await tutorial.FinishAsync();
        var cut = Render<Shared.TutorialChecklist>();

        var icon = cut.Find("[data-abom-tutorial-start='orientation']");
        Assert.Equal(MudBlazor.Icons.Material.Outlined.RestartAlt,
            cut.FindComponents<MudBlazor.MudIconButton>()[0].Instance.Icon);
        Assert.Empty(icon.TextContent.Trim());
        Assert.Equal("Restart this tutorial.", icon.Closest("[data-abom-tip]")!.GetAttribute("data-abom-tip"));
        Assert.Contains("Completed", cut.Find("[data-abom-tutorial-lesson='orientation']").TextContent);
        icon.Click();
        Assert.Equal(0, tutorial.StepIndex);
    }

    [Fact]
    public void TheHeaderKeepsDiscoveryButHasNoTutorialCheckbox()
    {
        Answer("api/discovery/status", """{"available":true,"status":{"state":"paused"},"frontier":{"done":10}}""");
        var cut = Render<Layout.MainLayout>();

        Assert.Empty(cut.FindAll("[data-abom-tutorial-toggle]"));
        Assert.NotNull(cut.Find("[data-abom-crawl]"));
    }

    [Fact]
    public void SettingsOffersTheSameTutorialListAsHome()
    {
        Answer("api/accounts", """{"authenticated":true,"login":"operator","accounts":[]}""");
        Answer("api/owned-scope", "[]");
        var cut = Render<Pages.Settings>();

        Assert.Equal(6, cut.FindAll("[data-abom-tutorial-title]").Count);
    }

    [Fact]
    public void AReadOnlyRequestPreviewCannotSaveOrFetchOrStartAnAgent()
    {
        var cut = Render<Shared.QueryRequestForm>(p => p
            .Add(c => c.ReadOnly, true)
            .Add(c => c.Request, new Models.QueryRequest
            {
                Title = "Read-only example", Description = "What to detect",
                Compliant = "permissions: read-all", NonCompliant = "permissions: write-all",
            }));

        Assert.True(cut.Find("fieldset").HasAttribute("disabled"));
        Assert.Empty(cut.FindAll("[data-abom-request-save], [data-abom-request-discard], [data-abom-example-read]"));
        Assert.NotNull(cut.Find("[data-abom-request-metadata]"));
        Assert.Empty(Sent);
        Assert.Empty(Services.GetRequiredService<TutorialService>().StartRefusal);
    }

    [Fact]
    public void AnOpenRequestBlocksTutorialStartUntilItIsClosed()
    {
        var cut = Render<Shared.QueryRequestForm>();
        Assert.Contains("request", Services.GetRequiredService<TutorialService>().StartRefusal);
        cut.Instance.Dispose();
        Assert.Empty(Services.GetRequiredService<TutorialService>().StartRefusal);
    }

    [Fact]
    public void LifecycleExplainsEveryExistingMarkAndEveryVersionStage()
    {
        var lesson = TutorialLessons.All.Single(l => l.Id == "lifecycle");
        foreach (var mark in new[] { "target", "help", "expected", "database", "ast", "compiles", "tests" })
            Assert.Single(lesson.Steps, s => s.Id == "mark-" + mark && s.Preview == mark);
        foreach (var stage in new[] { "dev", "test", "extended-test", "review", "deploy" })
            Assert.Single(lesson.Steps, s => s.Id == "stage-" + stage);
        Assert.All(TutorialLessons.All, l =>
        {
            Assert.Equal(l.Steps.Count, l.Steps.Select(s => s.Id).Distinct().Count());
            Assert.All(l.Steps, s => Assert.False(string.IsNullOrWhiteSpace(s.Text)));
        });
    }

    [Fact]
    public async Task AnUnsavedOwnershipEditCannotBeDiscardedByStartingATour()
    {
        Answer("api/accounts", """{"authenticated":true,"login":"operator","accounts":[]}""");
        Answer("api/owned-scope", """[{"owner":"acme","kind":"organization","enabled":true}]""");
        Answer("api/owner-candidates", "[]");
        var tutorial = Services.GetRequiredService<TutorialService>();
        await tutorial.InitializeAsync();
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-undeclare]")));
        cut.Find("[data-abom-undeclare]").Click();

        await tutorial.StartAsync("orientation");

        Assert.Null(tutorial.Active);
        Assert.Contains("ownership", tutorial.StartRefusal);
    }
}
