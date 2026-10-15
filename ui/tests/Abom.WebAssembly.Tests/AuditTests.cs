using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class AuditTests : AbomTestContext
{
    [Fact]
    public void InvalidAuditFilterDisplaysTheServersCorrectiveReason()
    {
        Refuse("api/authoring-events", System.Net.HttpStatusCode.BadRequest, "from must be an RFC3339 timestamp");
        var cut = Render<Shared.AuditEvents>(p => p.Add(c => c.Filter, "from=bad"));
        cut.WaitForAssertion(() => Assert.Contains("from must be an RFC3339 timestamp", cut.Markup));
        Assert.DoesNotContain("No recorded events", cut.Markup);
    }

    [Fact]
    public void EventRowsAreScannableAndBothEndsHavePaging()
    {
        Answer("api/authoring-events", """
            {"items":[{"rowId":9,"sessionId":1,"at":"2026-09-01T12:30:00Z",
            "hook":"pre-tool-use","tool":"edit","subject":"src/Check.ql","reason":"Marked fixtures required",
            "decision":"refused","purpose":"authoring","capture":{}}],
            "total":605,"hasMore":true,"nextCursor":"next","through":605}
            """);
        var cut = Render<Shared.AuditEvents>();
        cut.WaitForAssertion(() => Assert.Equal(2, cut.FindAll("[data-abom-audit-pager]").Count));
        var summary = cut.Find("[data-abom-event='9'] > summary");
        Assert.Contains("Tool permission check", summary.TextContent);
        Assert.Contains("src/Check.ql", summary.TextContent);
        Assert.Contains("Marked fixtures required", summary.TextContent);
        Assert.Contains("Sep", summary.TextContent);
        Assert.DoesNotContain("pre-tool-use", summary.TextContent);
        Assert.DoesNotContain("duration not measured", summary.TextContent);
        Assert.NotEmpty(summary.QuerySelectorAll(".abom-audit-outcome svg"));
        Assert.Contains("not measured", cut.Find("[data-abom-duration-help]").TextContent);
    }

    [Fact]
    public void LatestPolicyIsReadableWithoutOpeningJsonAndSummaryShowsItsLimits()
    {
        Answer("api/authoring-sessions/1", """
            {"rowId":1,"purpose":"authoring","policies":[
              {"rowId":2,"capture":{"policy":{"timeoutMs":600000,"creditsKnown":true,"maxAiCredits":2}}},
              {"rowId":8,"capture":{"policy":{"timeoutMs":2700000,"timeoutSource":"default","creditsKnown":true,
                "maxAiCredits":12,"tools":["builtin:edit"],"runtime":{"sdk":"v1.0.14"},
                "bounds":{"target":"src/Check.ql","writable":{"src/Check.ql":"query"},
                "fixtureRoot":"test/Check","fixtureExtensions":[".yml"],"readOnly":["library"],
                "denied":["shell"],"queryHeld":"Marked fixtures required"}}}}
            ],"relations":[{"kind":"worked request","requestId":3}]}
            """);
        var cut = Render<Shared.SessionPolicy>(p => p.Add(c => c.SessionId, 1));
        cut.WaitForAssertion(() => Assert.Contains("45 min", cut.Find("[data-abom-policy-latest]").TextContent));
        Assert.Contains("Capped at 12 AI credits", cut.Find("[data-abom-policy-latest]").TextContent);
        Assert.Contains("src/Check.ql", cut.Find("[data-abom-policy-latest]").TextContent);
        Assert.Contains("Available tools", cut.Find("[data-abom-policy-latest]").TextContent);
        Assert.Contains("Editable files", cut.Find("[data-abom-policy-latest]").TextContent);
        Assert.Contains("v1.0.14", cut.Find("[data-abom-policy-latest]").TextContent);
        Assert.Contains("Worked request: request #3", cut.Find(".abom-audit-relations").TextContent);
        Assert.Single(cut.FindAll("[data-abom-policy-history]"));
        Assert.False(cut.Find("[data-abom-policy-history]").HasAttribute("open"));
    }

    [Fact]
    public void TestEvidenceIsReadableAndUnknownCountsStayUnknown()
    {
        Answer("api/authoring-events", """
            {"items":[{"rowId":9,"sessionId":1,"hook":"test-evidence","decision":"failed",
            "capture":{"test":{"passed":false,"failureStage":"RESULT","total":3,"failed":1,
            "missing":2,"unexpected":null,"queryId":"custom/check","queryVersion":"abc123",
            "cliVersion":"2.27.2","fixturesDigest":"fixture-hash","detail":"measured comparison"}}}],
            "total":1,"through":9}
            """);
        var cut = Render<Shared.AuditEvents>(p => p.Add(c => c.ReproductionView, true));
        cut.WaitForAssertion(() => Assert.Single(cut.FindAll("[data-abom-test-evidence]")));
        var evidence = cut.Find("[data-abom-test-evidence]");
        Assert.Contains("Result comparison failed", evidence.TextContent);
        Assert.Contains("2 of 3 passed", evidence.TextContent);
        Assert.Contains("Missing results", evidence.TextContent);
        Assert.Contains("Unexpected results", evidence.TextContent);
        Assert.Contains("Not recorded", evidence.TextContent);
        Assert.Contains("CodeQL CLI", evidence.TextContent);
        Assert.Contains("fixture-hash", evidence.TextContent);
        Assert.Empty(evidence.QuerySelectorAll("pre"));
        Assert.True(cut.Find("[data-abom-event='9']").HasAttribute("open"));
    }

    [Fact]
    public void FilterControlsNameTheQuestionAndPreserveHistoricOutcomes()
    {
        ((Microsoft.AspNetCore.Components.NavigationManager)Services.GetService(
            typeof(Microsoft.AspNetCore.Components.NavigationManager))!).NavigateTo("/codeql?tab=audit&decision=historic-value");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        var cut = Render<Shared.AuditLog>();
        Assert.Contains("Search events", cut.Markup);
        var selects = cut.FindComponents<MudBlazor.MudSelect<string>>().ToDictionary(s => s.Instance.Label ?? "");
        Assert.Contains("Session purpose", selects.Keys);
        Assert.Contains("Event order", selects.Keys);
        // A value the record holds and no choice offers is kept, and shown as recorded.
        Assert.Equal("historic-value", selects["Outcome"].Instance.Value);
        Assert.Contains("historic-value (recorded value)", selects["Outcome"].Markup);
    }

    [Fact]
    public void PreviousPageKeepsTheOriginalCutoff()
    {
        Answer("cursor=next", """{"items":[],"total":605,"hasMore":false,"through":605}""");
        Answer("api/authoring-events", """{"items":[],"total":605,"hasMore":true,"nextCursor":"next","through":605}""");
        var cut = Render<Shared.AuditEvents>();
        cut.WaitForAssertion(() => Assert.Contains("605 events", cut.Markup));
        cut.Find("[data-abom-audit-next]").Click();
        cut.Find("[data-abom-audit-previous]").Click();
        cut.WaitForAssertion(() => Assert.Contains("through=605", Sent.Last(r => !r.Url.Contains("limit=1")).Url));
    }

    [Fact]
    public void AuditFailureIsNotAnEmptyLog()
    {
        Fail("api/authoring-events", System.Net.HttpStatusCode.InternalServerError);
        var cut = Render<Shared.AuditEvents>();
        cut.WaitForAssertion(() => Assert.Contains("Cannot load the audit log", cut.Markup));
        Assert.DoesNotContain("No recorded events", cut.Markup);
    }

    // A session, request or run that is not recorded is the filters' answer, not an outage: "the
    // server is unavailable" sent a reader to retry a link that can never load.
    [Fact]
    public void AnUnrecordedSessionIsNamedRatherThanReadAsAnOutage()
    {
        Refuse("api/authoring-events", System.Net.HttpStatusCode.NotFound, "no session 999 is recorded");
        var cut = Render<Shared.AuditEvents>(p => p.Add(c => c.Filter, "session=999"));
        cut.WaitForAssertion(() => Assert.Contains("no session 999 is recorded", cut.Markup));
        Assert.DoesNotContain("server is unavailable", cut.Markup);
        Assert.DoesNotContain("No recorded events", cut.Markup);
    }

    [Fact]
    public void ChangingFiltersCancelsAStaleAnswer()
    {
        var release = Withhold("session=1");
        Answer("session=1", """{"items":[],"total":987,"through":987}""");
        Answer("session=2", """{"items":[],"total":2,"through":2}""");
        var cut = Render<Shared.AuditEvents>(p => p.Add(c => c.Filter, "session=1"));
        cut.Render(p => p.Add(c => c.Filter, "session=2"));
        release();
        cut.WaitForAssertion(() => Assert.Contains("of 2 events", cut.Markup));
        // The count, not the whole markup: a tooltip's generated id can hold any digits.
        Assert.DoesNotContain("987", cut.Find("[data-abom-audit-count]").TextContent);
        Assert.DoesNotContain("987", cut.Find("[data-abom-audit-pager='bottom']").ParentElement!.TextContent);
    }

    [Fact]
    public void AuditCountsEventsAndExposesEverySafeDetail()
    {
        Answer("api/authoring-events", """
            {"items":[{"rowId":9,"sessionId":1,"hook":"pre-tool-use","tool":"view",
            "decision":"allowed","detail":"safe structured arguments","purpose":"authoring",
            "capture":{"correlation":"uncorrelated hook observation"}}],
            "total":605,"hasMore":true,"nextCursor":"next","through":605,"coverage":"Events, not calls."}
            """);
        var cut = Render<Shared.AuditEvents>();
        cut.WaitForAssertion(() => Assert.Contains("Showing 1 of 605 events", cut.Markup));
        Assert.Contains("safe structured arguments", cut.Find("[data-abom-event='9']").TextContent);
        Assert.Contains("uncorrelated hook observation", cut.Markup);
        Assert.False(cut.Find("[data-abom-audit-next]").HasAttribute("disabled"));
    }

    [Fact]
    public void HistoricSessionPolicyIsUnknownNotUncapped()
    {
        Answer("api/authoring-sessions/1", """
            {"rowId":1,"purpose":"authoring","state":"finished","policies":[],"relations":[],
            "coverage":"Historical policy not recorded."}
            """);
        var cut = Render<Shared.SessionPolicy>(p => p.Add(c => c.SessionId, 1));
        cut.WaitForAssertion(() => Assert.Contains("Limits and permissions were not recorded", cut.Markup));
        Assert.DoesNotContain("No AI credit cap", cut.Markup);
    }

    // A timeout arrives as a cancellation that nobody asked for. Only an answer the reader
    // superseded may pass in silence; a stalled server has to read as a failure, or an empty page
    // reads as a log with nothing in it.
    [Fact]
    public void AnAuditLogThatTimesOutSaysItCouldNotLoad()
    {
        TimeOut("api/authoring-events");
        var cut = Render<Shared.AuditEvents>();
        cut.WaitForAssertion(() => Assert.Contains("Cannot load the audit log", cut.Markup));
    }

    [Fact]
    public void SessionLimitsThatTimeOutSaySoRatherThanShowingNothing()
    {
        TimeOut("api/authoring-sessions/1");
        var cut = Render<Shared.SessionPolicy>(p => p.Add(c => c.SessionId, 1));
        cut.WaitForAssertion(() => Assert.Contains("Cannot load recorded session limits and permissions", cut.Markup));
    }

    [Fact]
    public void RelatedSessionsThatTimeOutAreNotReadAsNoAgent()
    {
        TimeOut("api/authoring-sessions?");
        var cut = Render<Shared.AuditSessions>(p => p.Add(c => c.RunId, 3));
        cut.WaitForAssertion(() => Assert.Contains("Cannot load related sessions", cut.Markup));
    }

    [Fact]
    public void FollowThatTimesOutSaysItStopped()
    {
        TimeOut("limit=1");
        Answer("api/authoring-events", """{"items":[],"total":0,"through":0}""");
        var cut = Render<Shared.AuditEvents>();
        cut.WaitForAssertion(() => Assert.Contains("0 events", cut.Markup));
        cut.WaitForAssertion(() => Assert.Contains("Follow stopped", cut.Markup), TimeSpan.FromSeconds(8));
    }
}
