using Abom.WebAssembly.Models;
using Abom.WebAssembly.Pages;
using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Explorer is the only surface that draws, so it is the only one that can say how a view was
/// drawn and the only one that can put it back. Inventory applies the predicate and ignores this
/// half entirely — a list has no grouping, no depth and no third dimension.
/// </summary>
public sealed class ExplorerPresentationTests : AbomTestContext
{
    private void StubGraphModule(Explorer.Counts? counts = null)
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(counts ?? new Explorer.Counts(0, 0, 0, 0));
        foreach (var name in new[]
                 { "setPalette", "setAnimate", "setGroupBy", "setCollapsed", "setView" })
        {
            module.SetupVoid(name, _ => true).SetVoidResult();
        }
    }

    [Theory]
    [InlineData("owner")]
    [InlineData("repo")]
    public void AccountlessImagesKeepTheirLegendAndAreNotCountedAsGroups(string grouping)
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule(new Explorer.Counts(4, 0, 0, 4));
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo($"/explore?kinds=docker_image&groupBy={grouping}&collapsed=true");

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
        {
            Assert.Single(page.FindAll("input[aria-label='Show Docker image nodes']"));
            Assert.DoesNotContain("docker image", string.Join(" ", page.FindAll(".abom-key-note").Select(n => n.TextContent)));
            Assert.Contains("<b>4 nodes</b> after grouping", page.Markup);
        });
    }

    [Fact]
    public async Task DockerImageLegendRemainsVisibleAtWorkflowFoldDepth()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();
        ArriveWithAScope();

        var page = Render<Explorer>();
        page.WaitForAssertion(() => Assert.NotEmpty(JSInterop.Invocations["loadFull"]));
        await page.InvokeAsync(() =>
            page.Instance.ApplyPresentation(Presentation.Parse("groupBy=workflow&collapsed=true")));

        page.WaitForAssertion(() =>
            Assert.Single(page.FindAll("input[aria-label='Show Docker image nodes']")));
    }

    /// <summary>
    /// Distance in the Priority view is the advisories unit's rank, so an action carrying the
    /// first-ranked advisory sits closest in even where a lower-ranked one reaches more.
    /// </summary>
    [Fact]
    public void ThePriorityViewPlacesActionsByTheAdvisoriesRank()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        Answer("api/remediation", """
            {"items":[
              {"ghsaId":"GHSA-high","severity":"high","directRepos":1,"transitiveRepos":0,
               "actions":["actions/cache"],"rank":1},
              {"ghsaId":"GHSA-low","severity":"low","directRepos":4,"transitiveRepos":0,
               "actions":["actions/checkout"],"rank":2}],"total":2,"unreached":0}
            """);
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?scope=owned&view=priority");

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.NotEmpty(JSInterop.Invocations["setPriorities"]));
        var urgency = (Dictionary<string, int>)
            JSInterop.Invocations["setPriorities"].Last().Arguments[1]!;
        Assert.True(urgency["actions/cache"] > urgency["actions/checkout"],
            $"cache {urgency["actions/cache"]} is not closer than checkout {urgency["actions/checkout"]}");
    }

    /// <summary>
    /// What the Explorer would save has to describe what is on screen. Reading it from anywhere
    /// but the page's own state would let a saved view record a picture nobody was looking at.
    /// </summary>
    [Fact]
    public void WhatTheExplorerWouldSaveDescribesWhatIsOnScreen()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?view=priority&groupBy=owner&hops=2&use3d=true");

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
        {
            var drawn = page.Instance.CurrentPresentation;
            Assert.Equal("priority", drawn.View);
            Assert.Equal("owner", drawn.GroupBy);
            Assert.Equal(2, drawn.Hops);
            Assert.True(drawn.Use3d);
        });
    }

    /// <summary>
    /// A drawing choice has to reach the address, or the link a reader shares shows the same
    /// nodes arranged some other way. These four never did: grouping, the third dimension,
    /// collapsing and the stable layout all changed the picture and left no trace.
    /// </summary>
    [Fact]
    public void ADrawingChoiceReachesTheAddress()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?groupBy=owner&use3d=true&collapsed=true&stableLayout=true");

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
        {
            var at = Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri;
            Assert.Contains("groupBy=owner", at);
            Assert.Contains("use3d=true", at);
            Assert.Contains("collapsed=true", at);
            Assert.Contains("stableLayout=true", at);
        });
    }

    /// <summary>
    /// A drawing choice is not a filter, and reaching the address must not make it one. The
    /// scope's own serializer refuses a term it does not know, so a presentation key leaking into
    /// it would be reported as an unknown filter on the next reload.
    /// </summary>
    [Fact]
    public void ADrawingChoiceNeverBecomesAFilterTerm()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?groupBy=owner&use3d=true&scope=owned");

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
        {
            var scope = Services.GetService<ScopeService>()!;
            Assert.Null(scope.LastError);
            Assert.DoesNotContain("groupBy", scope.ToQueryString());
            Assert.DoesNotContain("use3d", scope.ToQueryString());
        });
    }

    /// A saved presentation put back on the page draws it the way it was saved.
    [Fact]
    public async Task ApplyingASavedPresentationRedrawsItThatWay()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();
        ArriveWithAScope();

        var page = Render<Explorer>();
        page.WaitForAssertion(() => Assert.NotEmpty(JSInterop.Invocations["loadFull"]));

        await page.InvokeAsync(() => page.Instance.ApplyPresentation(
            Presentation.Parse("view=priority&groupBy=owner&hops=3")));

        page.WaitForAssertion(() =>
        {
            var drawn = page.Instance.CurrentPresentation;
            Assert.Equal("priority", drawn.View);
            Assert.Equal("owner", drawn.GroupBy);
            Assert.Equal(3, drawn.Hops);
        });
    }

    /// <summary>
    /// A view saved with no presentation leaves the reader's own choices alone. Treating absent
    /// as "every default" would reset the picture of anybody applying a view saved before this
    /// existed — which is every view saved so far.
    /// </summary>
    [Fact]
    public async Task AViewWithNoSavedPresentationLeavesTheDrawingAlone()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?view=priority&groupBy=owner");

        var page = Render<Explorer>();
        page.WaitForAssertion(() => Assert.Equal("priority", page.Instance.CurrentPresentation.View));

        await page.InvokeAsync(() => page.Instance.ApplyPresentation(Presentation.Parse("")));

        page.WaitForAssertion(() =>
        {
            Assert.Equal("priority", page.Instance.CurrentPresentation.View);
            Assert.Equal("owner", page.Instance.CurrentPresentation.GroupBy);
        });
    }
}
