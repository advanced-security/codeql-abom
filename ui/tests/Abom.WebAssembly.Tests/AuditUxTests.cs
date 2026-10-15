using Abom.WebAssembly.Pages;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using MudBlazor;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// How an agent session and its audit trail read: one card per session with what it is held to
/// beside what it took, details inside the card they belong to, and an audit log whose search and
/// filters apply as they are chosen and say what each scope is.
/// </summary>
public class AuditUxTests : AbomTestContext
{
    private static string Session(string state = "finished", string startedAt = "2026-10-14T12:57:24Z",
        string endedAt = "2026-10-14T13:05:25Z", string policies = """
            [{"rowId":448,"capture":{"policy":{"timeoutMs":2700000,"timeoutSource":"configured","creditsKnown":true}}}]
            """) => $$"""
        {"rowId":3,"requestId":3,"purpose":"authoring","state":"{{state}}","startedBy":"data-douser",
         "model":"claude-opus-5","startedAt":"{{startedAt}}","endedAt":"{{endedAt}}","events":289,"refused":15,
         "modelCalls":45,"inputTokens":1783281,"outputTokens":33921,"nanoAiu":209640000000,
         "detail":"I've exhausted the options available to me.","policies":{{policies}},
         "relations":[{"kind":"worked request","requestId":3},
           {"kind":"version evaluated by run","runId":2,"queryId":"acme/egress","version":"7c60"}],
         "coverage":"Events, not calls."}
        """;

    private IRenderedComponent<Shared.SessionCard> Card(string json) =>
        Render<Shared.SessionCard>(p => p
            .Add(c => c.SessionId, 3)
            .Add(c => c.Metadata, System.Text.Json.JsonSerializer.Deserialize<Models.AuditSession>(json,
                new System.Text.Json.JsonSerializerOptions(System.Text.Json.JsonSerializerDefaults.Web))));

    private Bunit.TestDoubles.BunitNavigationManager Nav =>
        Services.GetRequiredService<Bunit.TestDoubles.BunitNavigationManager>();

    // The limit is only useful beside what the session used of it.
    [Fact]
    public void ASessionsDurationIsMeasuredAgainstItsLimit()
    {
        var cut = Card(Session());

        var duration = cut.Find("[data-abom-session-duration]");
        Assert.Contains("8 min 1 s of 45 min", duration.TextContent);
        Assert.Equal("18", duration.GetAttribute("data-abom-session-duration-percent"));
        Assert.NotNull(duration.QuerySelector("[role=progressbar]"));
    }

    [Fact]
    public void ARunningSessionsDurationIsMeasuredSoFar()
    {
        var started = DateTimeOffset.UtcNow.AddMinutes(-5).ToString("O");
        var cut = Card(Session(state: "running", startedAt: started, endedAt: ""));

        var duration = cut.Find("[data-abom-session-duration]").TextContent;
        Assert.Contains("Running", duration);
        Assert.Contains("5 min", duration);
        Assert.Contains("of 45 min", duration);
    }

    private static T Read<T>(string json) => System.Text.Json.JsonSerializer.Deserialize<T>(json,
        new System.Text.Json.JsonSerializerOptions(System.Text.Json.JsonSerializerDefaults.Web))!;

    private static string StateOf(IRenderedComponent<Shared.SessionCard> cut) =>
        cut.Find("[data-abom-session]").GetAttribute("data-abom-session-state") ?? "";

    // The Audit log's card reads its own session, and nothing else on the page says how a running
    // one stands. Read once, it said "running" and counted on long after the session had ended.
    [Fact]
    public void ARunningSessionACardReadsForItselfIsReadAgainUntilItEnds()
    {
        var started = DateTimeOffset.UtcNow.AddMinutes(-1).ToString("O");
        Answer("api/authoring-sessions/3", Session(state: "running", startedAt: started, endedAt: ""));
        var cut = Render<Shared.SessionCard>(p => p.Add(c => c.SessionId, 3));
        cut.WaitForAssertion(() => Assert.Equal("running", StateOf(cut)));
        Assert.True(cut.Instance.Watching, "a running session's card does not read it again");

        Answer("api/authoring-sessions/3", Session(startedAt: started, endedAt: DateTimeOffset.UtcNow.ToString("O")));

        cut.WaitForAssertion(() => Assert.Equal("finished", StateOf(cut)), TimeSpan.FromSeconds(8));
        cut.WaitForAssertion(() => Assert.False(cut.Instance.Watching, "an ended session is still being read"));
    }

    // A run's related sessions are listed from one read, so a running one there is read again too,
    // and what the card read itself is not replaced by the older listing.
    [Fact]
    public void ARunningSessionGivenByAListReadOnceIsStillReadAgain()
    {
        Answer("api/authoring-sessions/3", Session(state: "running", endedAt: ""));
        var cut = Card(Session(state: "running", startedAt: DateTimeOffset.UtcNow.AddMinutes(-1).ToString("O"), endedAt: ""));

        Assert.True(cut.Instance.Watching, "a running session listed once is never read again");
    }

    // Under its request, the request's own list is re-read while the session runs.
    [Fact]
    public void ASessionItsRequestFollowsIsLeftToTheRequest()
    {
        var running = Session(state: "running", startedAt: DateTimeOffset.UtcNow.AddMinutes(-1).ToString("O"), endedAt: "");
        var cut = Render<Shared.SessionCard>(p => p
            .Add(c => c.SessionId, 3)
            .Add(c => c.Summary, Read<Models.AuthoringSession>(running))
            .Add(c => c.Metadata, Read<Models.AuditSession>(running)));

        Assert.False(cut.Instance.Watching, "a session its request follows is read twice over");
    }

    [Fact]
    public void AnEndedSessionIsNotReadAgain()
    {
        var cut = Card(Session());

        Assert.False(cut.Instance.Watching, "an ended session is still being read");
    }

    // A session that is not recorded is said to be so: "cannot load its limits" read as an outage,
    // under an address that can never load.
    [Fact]
    public void ACardForAnUnrecordedSessionSaysSoRatherThanThatItCouldNotLoad()
    {
        Refuse("api/authoring-sessions/999", System.Net.HttpStatusCode.NotFound, "no session 999 is recorded");

        var cut = Render<Shared.SessionCard>(p => p.Add(c => c.SessionId, 999));

        cut.WaitForAssertion(() => Assert.Contains("No session #999 is recorded", cut.Markup));
        Assert.DoesNotContain("Cannot load", cut.Markup);
    }

    [Fact]
    public void ASessionWithNoRecordedLimitStillSaysHowLongItTook()
    {
        var cut = Card(Session(policies: "[]"));

        var duration = cut.Find("[data-abom-session-duration]");
        Assert.Contains("8 min 1 s", duration.TextContent);
        Assert.Contains("limit not recorded", duration.TextContent);
        Assert.Null(duration.QuerySelector("[role=progressbar]"));
    }

    // The cap is Copilot's, on what one session may spend; ABOM's name is already on every page.
    [Fact]
    public void AnUncappedSessionSaysSoInCopilotsTerms()
    {
        var cut = Card(Session());

        var credits = cut.Find("[data-abom-session-credits]").TextContent;
        Assert.Contains("No AI credit cap", credits);
        Assert.DoesNotContain("ABOM", cut.Markup);
        Assert.Contains("209.64 AI units", cut.Find("[data-abom-session-spend]").TextContent);
    }

    // A cost nobody reported is not a session that cost nothing, and it reads like the facts beside it.
    [Fact]
    public void UsageNobodyReportedSaysWhoDidNotReportIt()
    {
        var cut = Card(Session().Replace("\"nanoAiu\":209640000000", "\"nanoAiu\":null")
            .Replace("\"inputTokens\":1783281", "\"inputTokens\":null").Replace("\"outputTokens\":33921", "\"outputTokens\":null"));

        Assert.Equal("Not reported by Copilot", cut.Find("[data-abom-session-spend]").TextContent.Trim());
    }

    // What a session was refused is the security record: the count opens those events.
    [Fact]
    public void ASessionsRefusalsOpenInTheAuditLog()
    {
        var cut = Card(Session());

        var refused = cut.Find("[data-abom-session-refused]");
        Assert.Equal("15 refused", refused.TextContent.Trim());
        Assert.Contains("session=3", refused.GetAttribute("href"));
        Assert.Contains("decision=refused", refused.GetAttribute("href"));
        Assert.False(string.IsNullOrWhiteSpace(refused.Closest("[data-abom-tip]")?.GetAttribute("data-abom-tip")));
    }

    [Fact]
    public void ASessionRefusedNothingHasNothingToOpen()
    {
        var cut = Card(Session().Replace("\"refused\":15", "\"refused\":0"));

        Assert.Empty(cut.FindAll("[data-abom-session-refused]"));
        Assert.Contains("0 refused", cut.Find("[data-abom-session-activity]").TextContent);
    }

    // Details belong to the session they describe, one at a time, inside its card.
    [Fact]
    public void ASessionsDetailsOpenInsideItsCardOneAtATime()
    {
        var cut = Card(Session());
        Assert.Empty(cut.FindAll("[role=tabpanel]"));

        cut.Find("[data-abom-session-tab='said']").Click();
        var said = cut.Find("[role=tabpanel]");
        Assert.NotNull(said.Closest("[data-abom-session='3']"));
        Assert.Contains("exhausted the options", said.TextContent);

        cut.Find("[data-abom-session-tab='limits']").Click();
        Assert.Single(cut.FindAll("[role=tabpanel]"));
        Assert.Equal("true", cut.Find("[data-abom-session-tab='limits']").GetAttribute("aria-selected"));
    }

    [Fact]
    public void AgentSessionsIsTitledAndHoldsACardPerSession()
    {
        Answer("api/query-requests/7/sessions", """
            {"sessions":[{"rowId":3,"requestId":7,"state":"finished","startedBy":"operator","model":"m",
              "startedAt":"2026-10-14T12:57:24Z","endedAt":"2026-10-14T13:05:25Z","events":289,"refused":15}],
             "inFlight":[]}
            """);
        Answer("api/query-requests/7/asts", """{"asts":[]}""");
        Answer("api/authoring-sessions", $$"""{"items":[{{Session()}}],"total":1}""");
        Answer("api/accounts", """{"authenticated":true,"login":"operator","accounts":[]}""");
        var cut = Render<Shared.RequestSessions>(p => p.Add(c => c.Request,
            new Models.QueryRequest { RowId = 7, Status = "open" }));

        cut.WaitForAssertion(() => cut.Find("[data-abom-session='3'] [data-abom-session-duration]"));
        Assert.Contains("Agent Sessions", cut.Find("[data-abom-request-sessions]").TextContent);
        Assert.Contains("of 45 min", cut.Find("[data-abom-session-duration]").TextContent);
        Assert.Empty(cut.FindAll("[data-abom-session-policy] > details"));
    }

    [Fact]
    public void FollowIsOnByDefault()
    {
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        var cut = Render<Shared.AuditEvents>();

        cut.WaitForAssertion(() => cut.Find("[data-abom-audit-follow]"));
        var follow = cut.Find("[data-abom-audit-follow]");
        var input = follow.LocalName == "input" ? follow : follow.QuerySelector("input")!;
        Assert.Equal("Follow", input.Closest(".mud-switch")!.TextContent.Trim());
        Assert.True(input.HasAttribute("checked"));
        // A poll that finds nothing newer renders nothing, so wait on the requests themselves.
        Assert.True(SpinWait.SpinUntil(() => Sent.ToArray().Any(r => r.Url.Contains("limit=1", StringComparison.Ordinal)),
            TimeSpan.FromSeconds(6)), "Follow, on by default, never asked for newer events.");
    }

    // Oldest first, the newest events land on the last page. Follow re-read that page with its own
    // cursor and no later cutoff, which returns the same page, so the newer events never appeared.
    [Fact]
    public void FollowGrowsTheLastOldestFirstPageWithTheNewerEvents()
    {
        Answer("limit=1", """{"items":[{"rowId":700,"sessionId":3,"hook":"message","decision":"noted","capture":{}}],"total":1,"through":700}""");
        Answer("cursor=next", """{"items":[{"rowId":605,"sessionId":3,"hook":"message","decision":"noted","capture":{}}],"total":2,"hasMore":false,"through":605}""");
        Answer("api/authoring-events", """{"items":[{"rowId":604,"sessionId":3,"hook":"message","decision":"noted","capture":{}}],"total":2,"hasMore":true,"nextCursor":"next","through":605}""");
        var cut = Render<Shared.AuditEvents>(p => p.Add(c => c.Filter, "session=3&order=asc"));
        cut.WaitForAssertion(() => Assert.False(cut.Find("[data-abom-audit-next]").HasAttribute("disabled")));

        cut.Find("[data-abom-audit-next]").Click();
        cut.WaitForAssertion(() => Assert.Contains("605", cut.Find("[data-abom-audit-count]").TextContent));

        Assert.True(SpinWait.SpinUntil(() => Sent.ToArray().Any(r => r.Url.Contains("cursor=next", StringComparison.Ordinal)
            && r.Url.Contains("through=700", StringComparison.Ordinal)), TimeSpan.FromSeconds(8)),
            "Follow never read the last page on to the newer cutoff");
        Assert.Empty(cut.FindAll("[data-abom-audit-newer]"));
    }

    [Fact]
    public void PagingRefreshAndExportAreIconsThatSayWhatTheyDo()
    {
        Answer("api/authoring-events", """{"items":[],"total":605,"hasMore":true,"nextCursor":"next","through":605}""");
        var cut = Render<Shared.AuditEvents>();

        cut.WaitForAssertion(() => cut.Find("[data-abom-audit-refresh]"));
        foreach (var element in new[] { "previous", "next", "refresh", "export" }
                     .Select(control => cut.Find($"[data-abom-audit-{control}]")))
        {
            Assert.Equal("", element.TextContent.Trim());
            Assert.NotNull(element.QuerySelector("svg"));
            Assert.False(string.IsNullOrWhiteSpace(element.Closest("[data-abom-tip]")?.GetAttribute("data-abom-tip")));
        }
    }

    [Fact]
    public void TypingASearchAppliesIt()
    {
        Nav.NavigateTo("/codeql?tab=audit");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        var cut = Render<Shared.AuditLog>();

        cut.Find("[data-abom-audit-search] input").Input("refused");

        cut.WaitForAssertion(() => Assert.Contains("text=refused", Nav.Uri), TimeSpan.FromSeconds(3));
    }

    [Fact]
    public void ChoosingAFilterAppliesIt()
    {
        Nav.NavigateTo("/codeql?tab=audit");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        var cut = Render<Shared.AuditLog>();
        Assert.DoesNotContain("Apply filters", cut.Markup);

        var outcome = cut.FindComponents<MudSelect<string>>().Single(s => s.Instance.Label == "Outcome");
        cut.InvokeAsync(() => outcome.Instance.ValueChanged.InvokeAsync("refused"));

        cut.WaitForAssertion(() => Assert.Contains("decision=refused", Nav.Uri));
    }

    [Fact]
    public void ARunScopeNamesTheRunAndItsQueries()
    {
        Nav.NavigateTo("/codeql?tab=audit&run=2");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        Answer("api/authoring-sessions", """{"items":[],"total":0}""");
        Answer("api/runs/2", """{"rowId":2,"state":"complete","queries":[{"queryId":"acme/egress","queryVersion":"7c60"}]}""");
        var cut = Render<Shared.AuditLog>();

        cut.WaitForAssertion(() => Assert.Contains("acme/egress", cut.Find("[data-abom-audit-chip='run']").TextContent));
        Assert.Contains("Run #2", cut.Find("[data-abom-audit-chip='run']").TextContent);
        Assert.DoesNotContain("Run number", cut.Markup);
    }

    [Fact]
    public void ARequestScopeNamesTheRequest()
    {
        Nav.NavigateTo("/codeql?tab=audit&request=3");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        Answer("api/authoring-sessions", """{"items":[],"total":0}""");
        Answer("api/query-requests", """{"requests":[{"rowId":3,"title":"Detect data egress","description":"d","status":"open"}]}""");
        var cut = Render<Shared.AuditLog>();

        cut.WaitForAssertion(() => Assert.Contains("Detect data egress", cut.Find("[data-abom-audit-chip='request']").TextContent));
        Assert.Contains("Request #3", cut.Find("[data-abom-audit-chip='request']").TextContent);
    }

    [Fact]
    public void ASessionScopeNamesItsPurposeAndRequest()
    {
        Nav.NavigateTo("/codeql?tab=audit&session=3");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        Answer("api/authoring-sessions/3", Session());
        var cut = Render<Shared.AuditLog>();

        cut.WaitForAssertion(() => Assert.Contains("request #3", cut.Find("[data-abom-audit-chip='session']").TextContent));
        Assert.Contains("Session #3", cut.Find("[data-abom-audit-chip='session']").TextContent);
        Assert.Contains("Query authoring", cut.Find("[data-abom-audit-chip='session']").TextContent);
    }

    [Fact]
    public void RemovingAChipRemovesThatFilterAlone()
    {
        Nav.NavigateTo("/codeql?tab=audit&run=2&decision=refused");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        Answer("api/authoring-sessions", """{"items":[],"total":0}""");
        Answer("api/runs/2", """{"rowId":2,"queries":[]}""");
        var cut = Render<Shared.AuditLog>();

        cut.Find("[data-abom-audit-chip='decision'] .mud-chip-close-button").Click();

        cut.WaitForAssertion(() => Assert.DoesNotContain("decision=", Nav.Uri));
        Assert.Contains("run=2", Nav.Uri);
    }

    // The order select read "As the scope or…", cut off, and said nothing of which order was in force.
    [Fact]
    public void EventOrderNamesTheOrderInForce()
    {
        Nav.NavigateTo("/codeql?tab=audit");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        var cut = Render<Shared.AuditLog>();

        var order = cut.FindComponents<MudSelect<string>>().Single(s => s.Instance.Label == "Event order");
        Assert.Equal("desc", order.Instance.Value);
        Assert.Equal("Newest first", order.Instance.Text);
        Assert.DoesNotContain("As the scope orders it", cut.Markup);

        cut.InvokeAsync(() => order.Instance.ValueChanged.InvokeAsync("asc"));
        cut.WaitForAssertion(() => Assert.Contains("order=asc", Nav.Uri));
    }

    [Fact]
    public void ASessionReadsOldestFirstAndChoosingThatAddsNoTerm()
    {
        Nav.NavigateTo("/codeql?tab=audit&session=3&order=desc");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        Answer("api/authoring-sessions/3", Session());
        var cut = Render<Shared.AuditLog>();

        var order = cut.FindComponents<MudSelect<string>>().Single(s => s.Instance.Label == "Event order");
        Assert.Equal("desc", order.Instance.Value);
        cut.InvokeAsync(() => order.Instance.ValueChanged.InvokeAsync("asc"));

        cut.WaitForAssertion(() => Assert.DoesNotContain("order=", Nav.Uri));
        Assert.Contains("session=3", Nav.Uri);
    }
}
