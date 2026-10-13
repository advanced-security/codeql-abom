using System.Net;
using System.Text.Json;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.AspNetCore.Components;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class TutorialStateTests : AbomTestContext
{
    private TutorialService Tutorial => Services.GetRequiredService<TutorialService>();

    [Fact]
    public async Task TheWelcomeIsOfferedOnceAndAcknowledgementIsNotCompletion()
    {
        await Tutorial.InitializeAsync();
        Assert.True(Tutorial.Enabled);
        Assert.True(Tutorial.ShowWelcome);
        Assert.False(Tutorial.IsReadOnly);

        await Tutorial.SetEnabledAsync(false);
        Assert.True(Tutorial.Acknowledged);
        Assert.False(Tutorial.Enabled);
        Assert.All(TutorialLessons.All, lesson => Assert.False(Tutorial.Completed(lesson)));
        Assert.Contains(JSInterop.Invocations, call => call.Identifier == "abomTutorial.save");
    }

    [Fact]
    public async Task FinishingMarksOnlyThisLessonAndRepeatDoesNotEraseIt()
    {
        await Tutorial.InitializeAsync();
        await Tutorial.StartAsync("orientation");
        Assert.True(Tutorial.IsReadOnly);
        Assert.True(Tutorial.Acknowledged);
        await Tutorial.ExitAsync();
        Assert.False(Tutorial.Completed(TutorialLessons.All[0]));

        await Tutorial.StartAsync("orientation");
        while (!Tutorial.LastStep) Tutorial.Next();
        await Tutorial.FinishAsync();
        Assert.True(Tutorial.Completed(TutorialLessons.All[0]));
        Assert.False(Tutorial.Enabled);

        await Tutorial.StartAsync("orientation");
        Assert.True(Tutorial.Completed(TutorialLessons.All[0]));
        Assert.Equal(0, Tutorial.StepIndex);
    }

    [Fact]
    public async Task RefreshRemembersCompletionButNeverRestartsAnOverlay()
    {
        var saved = JsonSerializer.Serialize(new
        {
            Version = 1, Acknowledged = true, Enabled = true,
            Completed = new Dictionary<string, int> { ["orientation"] = TutorialLessons.All[0].Version, ["new-rule"] = 0 },
        });
        JSInterop.Setup<string?>("abomTutorial.load", _ => true).SetResult(saved);
        await Tutorial.InitializeAsync();

        Assert.True(Tutorial.Acknowledged);
        Assert.False(Tutorial.Enabled);
        Assert.Null(Tutorial.Active);
        Assert.True(Tutorial.Completed(TutorialLessons.All[0]));
        Assert.False(Tutorial.Completed(TutorialLessons.All.Single(l => l.Id == "new-rule")));
        Assert.Contains(Services.GetRequiredService<NavigationManager>().BaseUri, Tutorial.StorageKey);
    }

    [Fact]
    public async Task CorruptPreferencesAreReportedRatherThanPretendingProgressWasSaved()
    {
        JSInterop.Setup<string?>("abomTutorial.load", _ => true).SetResult("{broken");
        await Tutorial.InitializeAsync();
        Assert.Contains("could not be read", Tutorial.Notice);
        Assert.True(Tutorial.ShowWelcome);
    }

    [Fact]
    public async Task AnEditBlocksStartingWithoutDiscardingIt()
    {
        await Tutorial.InitializeAsync();
        using (Tutorial.BlockStart("Finish or discard your request first."))
        {
            await Tutorial.StartAsync("new-rule");
            Assert.Null(Tutorial.Active);
            Assert.Contains("request", Tutorial.Notice);
        }
        await Tutorial.StartAsync("new-rule");
        Assert.NotNull(Tutorial.Active);
    }

    [Fact]
    public async Task UnexpectedNavigationEndsTheTourWithoutOverwritingTheDestination()
    {
        await Tutorial.InitializeAsync();
        var nav = Services.GetRequiredService<NavigationManager>();
        nav.NavigateTo("/inventory?unit=advisories");
        await Tutorial.StartAsync("orientation");
        Tutorial.Navigate("/");
        Assert.NotNull(Tutorial.Active);
        nav.NavigateTo("/settings");
        Assert.Null(Tutorial.Active);
        Assert.EndsWith("/settings", nav.Uri);
    }

    [Fact]
    public async Task AllControlRequestsAreRefusedBeforeDispatchAndReadsStillWork()
    {
        await Tutorial.InitializeAsync();
        await Tutorial.StartAsync("orientation");
        Answer("api/summary", "{}");
        var http = Services.GetRequiredService<HttpClient>();

        foreach (var method in new[] { HttpMethod.Post, HttpMethod.Put, HttpMethod.Delete, HttpMethod.Patch })
        {
            using var request = new HttpRequestMessage(method, "api/tutorial-guard-test");
            var error = await Assert.ThrowsAsync<HttpRequestException>(
                () => http.SendAsync(request, Xunit.TestContext.Current.CancellationToken));
            Assert.Contains("Exit the tutorial", error.Message);
        }
        Assert.Empty(Sent);
        Assert.Equal(HttpStatusCode.OK, (await http.GetAsync("api/summary", Xunit.TestContext.Current.CancellationToken)).StatusCode);
        Assert.Single(Sent);
    }

    [Fact]
    public async Task ASubmittedRunKeepsItsCancelControlsAvailableUntilItFinishes()
    {
        AnswerWrite("api/runs", """{"rowId":42,"state":"running"}""");
        await Tutorial.InitializeAsync();
        var api = Services.GetRequiredService<AbomApi>();
        await api.StartAnalysisRunAsync("", [], "operator", false, Xunit.TestContext.Current.CancellationToken);

        await Tutorial.StartAsync("orientation");

        Assert.Null(Tutorial.Active);
        Assert.Contains("run", Tutorial.StartRefusal);
    }

    [Fact]
    public async Task ARecoveredOperationDoesNotLeaveATutorialUnavailableNoticeBehind()
    {
        await Tutorial.InitializeAsync();
        var finished = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var reads = 0;
        Tutorial.WatchWork("test", "Wait for the run.", _ => Task.FromResult<bool?>(++reads == 1 ? null : false));
        Tutorial.Changed += () =>
        {
            if (reads > 1 && Tutorial.StartRefusal.Length == 0) finished.TrySetResult();
        };

        await finished.Task.WaitAsync(TimeSpan.FromSeconds(15), Xunit.TestContext.Current.CancellationToken);

        Assert.Equal(2, reads);
        Assert.Empty(Tutorial.Notice);
    }

    [Fact]
    public async Task AnUnfinishedLessonResumesOnlyWhenExplicitlySelected()
    {
        await Tutorial.InitializeAsync();
        await Tutorial.StartAsync("orientation");
        Tutorial.Next();
        Tutorial.Next();
        await Tutorial.ExitAsync();
        Assert.Null(Tutorial.Active);
        Assert.False(Tutorial.Enabled);

        await Tutorial.StartAsync("lifecycle");
        Tutorial.Next();
        await Tutorial.ExitAsync();
        await Tutorial.StartAsync("orientation");

        Assert.Equal(2, Tutorial.StepIndex);
        Assert.False(Tutorial.Completed(TutorialLessons.All[0]));
    }
}
