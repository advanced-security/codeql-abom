using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.AspNetCore.Components;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class DemoTests : AbomTestContext
{
    private const string DemoInstance = """
        {"mode":"demo","dataset":"demo-abc","scenario":"corpus-1","authoring":"unavailable",
         "authoringReason":"no installed ql-mcp was named","repositories":"simulated"}
        """;

    private async Task<InstanceService> Loaded(string json)
    {
        Answer("api/instance", json);
        var instance = Services.GetRequiredService<InstanceService>();
        await instance.LoadAsync();
        return instance;
    }

    // Demo is a fact about the server, stated on every page and never dismissible: a reader who
    // forgets it would take fictional repositories and practice findings for their own.
    [Fact]
    public async Task ADemoInstanceSaysSoOnEveryPageAndCannotBeDismissed()
    {
        await Loaded(DemoInstance);
        var cut = Render<Shared.DemoBanner>();
        var banner = cut.Find("[data-abom-demo-banner]");
        Assert.Contains("Demo", banner.TextContent);
        Assert.Contains("synthetic training data", banner.TextContent);
        Assert.Contains("Real CodeQL", cut.Find("[data-abom-demo-fact='codeql']").TextContent);
        Assert.Contains("simulated", cut.Find("[data-abom-demo-fact='repositories']").TextContent);
        Assert.Contains("unavailable", cut.Find("[data-abom-demo-fact='authoring']").TextContent);
        Assert.Empty(cut.FindAll("[data-abom-demo-banner] button"));
        Assert.DoesNotContain("checkbox", cut.Markup);
    }

    [Fact]
    public async Task AnOrdinaryInstanceShowsNoDemoBanner()
    {
        await Loaded("""{"mode":"normal"}""");
        var cut = Render<Shared.DemoBanner>();
        Assert.Empty(cut.FindAll("[data-abom-demo-banner]"));
    }

    // Progress against one practice dataset must not mark lessons done against another, or against
    // an organization's own data in the same browser.
    [Fact]
    public async Task TutorialProgressInADemoIsKeptApartFromEveryOtherInstance()
    {
        var tutorial = Services.GetRequiredService<TutorialService>();
        var ordinary = tutorial.StorageKey;
        await tutorial.InitializeAsync("demo-abc");
        Assert.NotEqual(ordinary, tutorial.StorageKey);
        Assert.Contains("demo-abc", tutorial.StorageKey);
    }

    [Fact]
    public async Task TheWelcomeInADemoSaysWhatIsRealAndWhatIsFictional()
    {
        await Loaded(DemoInstance);
        var tutorial = Services.GetRequiredService<TutorialService>();
        await tutorial.InitializeAsync("demo-abc");
        var cut = Render<Shared.TutorialHost>();
        var welcome = cut.Find("[data-abom-tutorial-welcome]");
        Assert.Contains("fictional", welcome.TextContent);
        Assert.Contains("real CodeQL", welcome.TextContent);
    }

    // After a walkthrough, a Demo offers to do the same steps for real; elsewhere it must not
    // suggest practicing on an organization's own data.
    [Theory]
    [InlineData("new-rule", "/codeql?tab=requests")]
    [InlineData("improve-rule", "/codeql?tab=catalog")]
    public async Task AQueryLessonInADemoEndsByOfferingToPracticeForReal(string lesson, string practice)
    {
        await Loaded(DemoInstance);
        var tutorial = Services.GetRequiredService<TutorialService>();
        await tutorial.InitializeAsync("demo-abc");
        await tutorial.StartAsync(lesson);
        while (!tutorial.LastStep) tutorial.Next();
        var cut = Render<Shared.TutorialHost>();
        // The browser reports the step's target; only the current generation's report is taken.
        for (var generation = 1; generation <= 4; generation++)
            await cut.Instance.TargetChanged(generation, "ready");
        var button = cut.Find("[data-abom-tutorial-practice]");
        await cut.InvokeAsync(() => button.Click());
        Assert.Null(tutorial.Active);
        Assert.True(tutorial.Completed(TutorialLessons.All.Single(l => l.Id == lesson)));
        Assert.EndsWith(practice, Services.GetRequiredService<NavigationManager>().Uri);
    }

    [Fact]
    public async Task AnOrdinaryInstanceOffersNoPracticeAfterAQueryLesson()
    {
        await Loaded("""{"mode":"normal"}""");
        var tutorial = Services.GetRequiredService<TutorialService>();
        await tutorial.InitializeAsync();
        await tutorial.StartAsync("new-rule");
        while (!tutorial.LastStep) tutorial.Next();
        var cut = Render<Shared.TutorialHost>();
        Assert.Empty(cut.FindAll("[data-abom-tutorial-practice]"));
    }

    // Demo never helps share practice data; the server refuses the download, and the interface
    // must not offer one that can only fail.
    [Fact]
    public async Task ADemoOffersNoBillOfMaterialsDownload()
    {
        await Loaded(DemoInstance);
        Answer("api/bom", """
            {"roots":["acme-corp/payments"],"generatedAt":"2026-09-28T00:00:00Z",
             "coverage":{"componentsInUse":4,"componentsAnalyzed":4,"referencesInUse":6,
               "referencesAnalyzed":6,"findings":2,"findingsUntraced":0,"findingsWithoutHash":0,
               "findingsWithoutDigest":0,"findingsWithoutProvider":0},
             "direct":3,"indirect":1,"deepestHop":2,"withFindings":1,"noneFound":3,"notExamined":0}
            """);
        var cut = Render<Shared.RepositoryDocument>(p => p.Add(c => c.Repository, "acme-corp/payments"));
        cut.WaitForAssertion(() => Assert.Contains("Demo", cut.Find("[data-abom-demo-withheld]").TextContent));
        Assert.Empty(cut.FindAll("[data-abom-document-format]"));
    }

    [Fact]
    public async Task TheDiscoveryTrackerInADemoSaysDiscoveryDoesNotRunHere()
    {
        await Loaded(DemoInstance);
        Answer("api/discovery/status", """{"available":false}""");
        var cut = Render<Shared.CrawlStatus>();
        cut.WaitForAssertion(() =>
            Assert.Contains("Demo", cut.Find("[data-abom-crawl-unavailable]").GetAttribute("aria-label")));
    }

    // An upstream Action is listed in a Demo but its repository is never served. Choosing it must say
    // so, and offer the Demo's repositories that use it, which is where an Actions query looks.
    [Fact]
    public async Task ChoosingAnUpstreamActionInADemoOffersTheRepositoriesThatUseIt()
    {
        await Loaded(DemoInstance);
        Answer("api/views", "[]");
        Answer("api/queries", """{"queries":[],"stages":["dev"]}""");
        Answer("api/accounts", """{"authenticated":true,"login":"demo-practitioner","accounts":[]}""");
        Answer("api/node/actions/checkout/-/uses", """
            {"key":"actions/checkout","items":[
              {"repository":"acme-corp/payments","origin":"seed","owned":true,"reference":"v4"},
              {"repository":"acme-corp/storefront","origin":"seed","owned":true,"reference":"v4"},
              {"repository":"acme-corp/payments","origin":"seed","owned":true,"reference":"v3"}],
             "total":3,"hiddenUpstream":0,"unanalyzed":0}
            """);
        Answer("api/databases?repositories=actions%2Fcheckout", """
            {"databases":[],"gaps":[],"mode":"regular",
             "unresolved":"actions/checkout is not in this Demo's simulated GitHub, so no run can build it",
             "outside":[{"repository":"actions/checkout","reason":"not a repository in this Demo's simulated GitHub"}]}
            """);
        Answer("api/databases", """
            {"databases":[],"gaps":[{"repository":"acme-corp/payments","reason":""},
              {"repository":"acme-corp/storefront","reason":""}],"mode":"regular","unresolved":"","outside":[]}
            """);
        Answer("api/actions", """
            {"items":[{"nodeKey":"actions/checkout","kind":"action","owner":"actions","repo":"checkout","origin":"",
              "dependentRepos":2,"dependentSteps":4,"distinctRefs":2,"findings":0,
              "severity":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"advisories":0,
              "analyzed":false,"refs":["v4"]}],"total":1}
            """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/inventory");
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => page.Find("[data-abom-action='run-queries']"));
        page.Find("[data-abom-action='run-queries']").Click();
        page.WaitForAssertion(() => page.Find("[data-abom-run-choose='actions/checkout'] input").Change(true));

        page.WaitForAssertion(() => Assert.Contains("actions/checkout",
            page.Find("[data-abom-run-size]").TextContent));
        page.Find("[data-abom-run-users]").Click();

        page.WaitForAssertion(() =>
        {
            Assert.Contains("2 chosen repositories", page.Find("[data-abom-run-chosen]").TextContent);
            Assert.Contains(Sent, r => r.Url.Contains("api/databases?repositories=acme-corp%2Fpayments,acme-corp%2Fstorefront"));
        });
        Assert.Empty(page.FindAll("[data-abom-run-users]"));
    }
}
