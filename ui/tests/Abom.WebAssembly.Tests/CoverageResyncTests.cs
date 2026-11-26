using System.Net;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Coverage is where a reader finds what failed, so it is where they resync it: the rows and the
/// declared accounts they choose, and nothing else. The only other answers were a whole tier, every
/// unavailable row, or one repository typed into Settings.
/// </summary>
public class CoverageResyncTests : AbomTestContext
{
    private const string NotInstalled = "the GitHub App is not installed on this account: advanced-security";

    public CoverageResyncTests()
    {
        Answer("api/discovery/suites", """{"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}""");
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().ToJson());
        Answer("api/frontier", """
            [{"nodeKey":"octo/app","ref":"","owner":"octo","repo":"app","status":"unavailable","depth":0,
              "attempts":3,"reason":"fatal: could not read Username","tier":"owned"},
             {"nodeKey":"actions/checkout","ref":"v4","owner":"actions","repo":"checkout","status":"done",
              "depth":1,"tier":"direct","analyzedAt":"2026-11-20T00:00:00Z"},
             {"nodeKey":"actions/checkout","ref":"v5","owner":"actions","repo":"checkout","status":"done",
              "depth":1,"tier":"direct","analyzedAt":"2026-11-20T00:00:00Z"}]
            """);
        Answer("api/owned-scope", $$$"""
            [{"owner":"octo","kind":"organization","enabled":true,"state":"confirmed",
              "enumeration":{"listedAt":"2026-11-23T00:00:00Z","failures":[]}},
             {"owner":"advanced-security","kind":"organization","enabled":true,"includesOnly":true,
              "includes":["codeql-abom-test1"],"state":"unknown",
              "enumeration":{"failures":[{"subject":"advanced-security","failedAt":"2026-11-24T09:00:00Z",
                "reason":"{{{NotInstalled}}}"}]}},
             {"owner":"fresh-org","kind":"organization","enabled":true,"state":"confirmed",
              "enumeration":{"failures":[]}},
             {"owner":"not-mine","kind":"organization","enabled":true,"state":"contradicted",
              "enumeration":{"listedAt":"2026-11-23T00:00:00Z","failures":[]}},
             {"owner":"switched-off","kind":"organization","enabled":false,"state":"confirmed",
              "enumeration":{"failures":[{"subject":"switched-off","failedAt":"2026-11-24T09:00:00Z","reason":"gone"}]}}]
            """);
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/actions", """{"items":[],"total":0}""");
        AnswerWrite("api/discovery/resync", $$$"""
            {"requeued":1,"queued":0,"waiting":0,"running":0,"namesNoRepository":0,"notFound":0,
             "repositories":["actions/checkout"],"owned":[],"expedited":1,"reread":[],
             "relisted":["advanced-security"],"state":{{{DiscoveryStateBuilder.Drained().ToJson()}}}}
            """);
    }

    private IRenderedComponent<Pages.Inventory> Coverage()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-unit='coverage']")));
        page.Find("[data-abom-unit='coverage']").Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-coverage-version]")));
        return page;
    }

    private static AngleSharp.Dom.IElement Input(AngleSharp.Dom.IElement e) =>
        e.LocalName == "input" ? e : e.QuerySelector("input")!;

    private IEnumerable<SentRequest> Resyncs =>
        Sent.Where(r => r.Method == "POST" && r.Url.Contains("api/discovery/resync", StringComparison.Ordinal));

    // A role that may not steer discovery may still choose rows, and is told what resync needs.
    [Fact]
    public void AResyncTheRoleDoesNotAllowStaysDisabledNamingTheRole()
    {
        Answer("api/me", """
            {"mode":"signed-in","user":{"id":"https://idp.example#s","name":"Grace Hopper","username":"","role":"viewer"},"may":[],
             "needs":{"abom/discovery-control":"operator"}}
            """);
        var page = Coverage();

        Input(page.Find("[data-abom-coverage-pick='actions/checkout@v4']")).Change(true);

        page.WaitForAssertion(() => Assert.Equal("Resync 1 chosen", page.Find("[data-abom-coverage-resync]").TextContent.Trim()));
        var resync = page.Find("[data-abom-coverage-resync]");
        Assert.True(resync.HasAttribute("disabled"));
        Assert.Contains("needs the operator role", resync.Closest("[data-abom-tip]")!.GetAttribute("data-abom-tip"));
    }

    // One reference of a dependency is one row and one unit of work; choosing v4 must not resync v5.
    [Fact]
    public void OnlyTheRowsAndAccountsChosenAreResynced()
    {
        var page = Coverage();
        var resync = page.Find("[data-abom-coverage-resync]");
        Assert.True(resync.HasAttribute("disabled"), "resync was offered with nothing chosen");

        Input(page.Find("[data-abom-coverage-pick='actions/checkout@v4']")).Change(true);
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-coverage-account-pick='advanced-security']")));
        Input(page.Find("[data-abom-coverage-account-pick='advanced-security']")).Change(true);
        page.WaitForAssertion(() => Assert.Equal("Resync 2 chosen", page.Find("[data-abom-coverage-resync]").TextContent.Trim()));
        page.Find("[data-abom-coverage-resync]").Click();

        page.WaitForAssertion(() => Assert.Single(Resyncs));
        var body = Resyncs.Single().Body;
        Assert.Contains("\"nodeKey\":\"actions/checkout\",\"ref\":\"v4\"", body);
        Assert.DoesNotContain("v5", body);
        Assert.DoesNotContain("octo/app", body);
        Assert.Contains("\"accounts\":[\"advanced-security\"]", body);
    }

    // The answer is said, not implied by a reload: what was put back, and what the next cycle reads.
    [Fact]
    public void AResyncSaysWhatItDidAndClearsTheChoice()
    {
        var page = Coverage();
        Input(page.Find("[data-abom-coverage-pick='actions/checkout@v4']")).Change(true);
        Input(page.Find("[data-abom-coverage-account-pick='advanced-security']")).Change(true);
        page.Find("[data-abom-coverage-resync]").Click();

        page.WaitForAssertion(() =>
        {
            var note = page.Find("[data-abom-coverage-resync-note]").TextContent;
            Assert.Contains("1 put back", note);
            Assert.Contains("advanced-security", note);
            Assert.Contains("listed again", note);
        });
        page.WaitForAssertion(() => Assert.True(page.Find("[data-abom-coverage-resync]").HasAttribute("disabled")));
    }

    // An account discovery could not list, or lists and cannot act on, holds no rows, so the grid
    // alone would say nothing about it. Coverage names it and why; one listed and confirmed, or
    // switched off, is not offered.
    [Fact]
    public void AnAccountDiscoveryCouldNotListSaysWhyBesideTheRows()
    {
        var page = Coverage();

        page.WaitForAssertion(() =>
            Assert.Contains(NotInstalled, page.Find("[data-abom-coverage-account='advanced-security']").TextContent));
        Assert.Contains("not listed yet", page.Find("[data-abom-coverage-account='fresh-org']").TextContent);
        // Listed without trouble and still nothing: the membership list leaves it out, so it roots nothing.
        Assert.Contains("roots nothing", page.Find("[data-abom-coverage-account='not-mine']").TextContent);
        Assert.Empty(page.FindAll("[data-abom-coverage-account='octo']"));
        Assert.Empty(page.FindAll("[data-abom-coverage-account='switched-off']"));
    }

    // Choosing every row shown is how "everything unavailable" is said once the state is chosen,
    // and changing the state starts the choice again rather than carrying rows nobody can see.
    [Fact]
    public void EveryRowShownCanBeChosenAndAChangeOfStateStartsAgain()
    {
        var page = Coverage();
        Input(page.Find("[data-abom-coverage-pick-all]")).Change(true);
        page.WaitForAssertion(() => Assert.Equal("Resync 3 chosen", page.Find("[data-abom-coverage-resync]").TextContent.Trim()));

        page.Find("[data-abom-coverage-state='unavailable']").Click();
        page.WaitForAssertion(() => Assert.True(page.Find("[data-abom-coverage-resync]").HasAttribute("disabled")));
    }

    [Fact]
    public void ARefusedResyncSaysWhy()
    {
        RefuseWrite("api/discovery/resync", HttpStatusCode.BadRequest,
            "advanced-security is declared but switched off, so discovery does not list it");
        var page = Coverage();
        Input(page.Find("[data-abom-coverage-account-pick='advanced-security']")).Change(true);
        page.Find("[data-abom-coverage-resync]").Click();

        page.WaitForAssertion(() =>
            Assert.Contains("switched off", page.Find("[data-abom-coverage-resync-note]").TextContent));
    }
}
