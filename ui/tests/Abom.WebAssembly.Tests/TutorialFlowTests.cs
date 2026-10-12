using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class TutorialFlowTests : AbomTestContext
{
    [Fact]
    public void TheOverviewFollowsTheWorkInsteadOfTouringTheGraph()
    {
        var overview = TutorialLessons.All.Single(l => l.Id == "orientation");
        Assert.Equal(new[]
        {
            "/settings", "/inventory?unit=coverage",
            "/codeql?tab=requests", "/codeql?tab=catalog", "/codeql?tab=runs", "/codeql?tab=suites",
            "/inventory", "/inventory?unit=dependencies", "$node", "/inventory?unit=workflows",
        }, overview.Steps.Select(s => s.Route));
        Assert.DoesNotContain(overview.Steps, step => step.Route.Contains("explor", StringComparison.Ordinal));
    }

    [Fact]
    public void LessonsUsePlainLanguageAndNoTutorialOnlyPickers()
    {
        var copy = string.Join(" ", TutorialLessons.All.SelectMany(l => l.Steps)
            .Select(s => $"{s.Title} {s.Text} {s.Detail}"));
        Assert.DoesNotContain("seed", copy, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("upstream provenance", copy, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain(TutorialLessons.All.SelectMany(l => l.Steps),
            s => s.Preview.EndsWith("-picker", StringComparison.Ordinal));
    }

    [Fact]
    public async Task ProgressUsesTheCompleteFixedStepListAndOffersRelatedLessonsAtTheEnd()
    {
        var service = Services.GetRequiredService<TutorialService>();
        await service.InitializeAsync();
        await service.StartAsync("orientation");
        var cut = Render<Shared.TutorialHost>();
        var total = service.Active!.Steps.Count;
        for (var step = 0; step < total; step++)
        {
            cut.WaitForAssertion(() =>
            {
                var progress = cut.Find("[data-abom-tutorial-progress]");
                Assert.Equal(total.ToString(), progress.GetAttribute("aria-valuemax"));
                Assert.Equal(step.ToString(), progress.GetAttribute("aria-valuenow"));
                Assert.DoesNotContain("Read explanation instead", cut.Markup);
                Assert.Empty(cut.FindAll("[data-abom-tutorial-search], [data-abom-tutorial-choice]"));
            });
            if (step + 1 < total) await cut.InvokeAsync(service.Next);
        }
        Assert.NotEmpty(cut.FindAll("[data-abom-tutorial-related]"));
    }

    [Fact]
    public void DashboardScopesHaveTheirOwnHeadingAfterTutorials()
    {
        Answer("api/summary", "{}");
        var cut = Render<Pages.Home>();
        Assert.Equal("Explore your inventory", cut.Find("[data-abom-home-scopes] h2").TextContent.Trim());
        Assert.True(cut.Markup.IndexOf("data-abom-tutorial-checklist", StringComparison.Ordinal)
            < cut.Markup.IndexOf("data-abom-home-scopes", StringComparison.Ordinal));
    }

    [Fact]
    public void FindingDetailsLinkToRecordedAssetsWithoutGuessingMissingOnes()
    {
        var cut = Render<Shared.FindingReviewDetail>(p => p.Add(c => c.Details, new Models.FindingDetails
        {
            Assets = [new() { Key = "acme/action", Kind = "action" }],
        }));
        Assert.Equal("/node/acme%2Faction", cut.Find("[data-abom-finding-asset]").GetAttribute("href"));
        cut.Render(p => p.Add(c => c.Details, new Models.FindingDetails()));
        Assert.Empty(cut.FindAll("[data-abom-finding-asset]"));
    }

    [Fact]
    public async Task RelatedTutorialsCompleteOnlyTheFinishedLessonWithoutAnExtraStep()
    {
        var service = Services.GetRequiredService<TutorialService>();
        await service.InitializeAsync();
        await service.StartAsync("orientation");
        while (!service.LastStep) service.Next();
        await service.FinishAndStartAsync("lifecycle");

        Assert.True(service.Completed(TutorialLessons.All.Single(l => l.Id == "orientation")));
        Assert.False(service.Completed(TutorialLessons.All.Single(l => l.Id == "lifecycle")));
        Assert.Equal("lifecycle", service.Active!.Id);
        Assert.Equal(0, service.StepIndex);
    }

    [Fact]
    public void InternalOriginValuesAreNotUsedAsFilterLabels()
    {
        Answer("api/filters", """[{"name":"origins","label":"Origin","arity":"list","values":["seed"],"surfaces":["inventory"]}]""");
        Services.GetRequiredService<ScopeService>().Set(new ViewSpec { Origins = ["seed"] });
        var cut = Render<Shared.FilterPicker>(p => p.Add(c => c.Surface, "inventory"));
        cut.WaitForAssertion(() =>
        {
            var chip = cut.Find("[data-abom-filter-chip='origins']");
            Assert.DoesNotContain("seed", chip.TextContent);
            Assert.DoesNotContain("seed", chip.GetAttribute("title"));
        });
    }
}
