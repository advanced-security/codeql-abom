using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// An agent works a request from its row, and everything it tried, was refused and printed is read
/// there, after the session as much as during it.
/// </summary>
public class RequestSessionsTests : AbomTestContext
{
    private const string SignedIn = """{"authenticated":true,"login":"operator","accounts":[]}""";
    private const string SignedOut = """{"authenticated":false,"login":"","accounts":[]}""";

    private static readonly Models.QueryRequest Request = new()
    {
        RowId = 7, Description = "report checkout pinned to a tag", Compliant = "c", NonCompliant = "n",
        Status = "open",
    };

    private const string NoSessions = """{"sessions":[],"inFlight":[]}""";
    private const string NoTrees = """{"asts":[]}""";

    private const string Finished = """
        {"sessions":[{"rowId":1,"requestId":7,"state":"finished","startedBy":"operator",
          "model":"claude-opus-5","startedAt":"2026-09-24T02:20:25Z","endedAt":"2026-09-24T02:22:31Z",
          "detail":"Stopped once the tests passed.","events":84,"refused":4}],
         "inFlight":[]}
        """;

    private const string Running = """
        {"sessions":[{"rowId":2,"requestId":7,"state":"running","startedBy":"operator",
          "model":"claude-opus-5","startedAt":"2026-09-24T02:20:25Z","endedAt":"","detail":"",
          "events":12,"refused":1}],
         "inFlight":[{"tool":"ql-mcp-codeql_test_run","subject":"test/CheckoutTagPin","step":"tests",
          "since":"2026-09-24T02:21:04Z"}]}
        """;

    private const string Timeline = """
        {"items":[
          {"rowId":37,"sessionId":1,"at":"2026-09-24T02:21:00Z","hook":"pre-tool-use","tool":"bash",
           "subject":"rm -rf ~","decision":"refused","step":"",
           "reason":"bash is not part of the authoring loop","detail":"","tookMs":null},
          {"rowId":43,"sessionId":1,"at":"2026-09-24T02:21:08Z","hook":"post-tool-use",
           "tool":"ql-mcp-codeql_test_extract","subject":"test/CheckoutTagPin","decision":"succeeded",
           "step":"database","reason":"","detail":"","tookMs":3356}],
         "total":2,"through":43}
        """;

    private const string Trees = """
        {"asts":[{"rowId":3,"sessionId":1,"requestId":7,"queryId":"acme/checkout-tag-pin",
          "printedAt":"2026-09-24T02:21:23Z","query":"PrintAST",
          "database":"test/CheckoutTagPin/CheckoutTagPin.testproj",
          "sourceFiles":".github/workflows/checkout.yml","testDigest":"abc","size":42,"truncated":false}]}
        """;

    private const string Tree = """
        {"rowId":3,"sessionId":1,"requestId":7,"queryId":"acme/checkout-tag-pin",
         "printedAt":"2026-09-24T02:21:23Z","query":"PrintAST",
         "database":"test/CheckoutTagPin/CheckoutTagPin.testproj",
         "sourceFiles":".github/workflows/checkout.yml","testDigest":"abc","size":42,"truncated":false,
         "body":"#10000 UsesStep\n  #10001 ScalarValue actions/checkout@v4\n"}
        """;

    private IRenderedComponent<Shared.RequestSessions> Render(string sessions, string trees = NoTrees,
        string identity = SignedIn, Models.AuthoringReadiness? readiness = null)
    {
        Answer("api/query-requests/7/sessions", sessions);
        Answer("api/query-requests/7/asts", trees);
        Answer("api/accounts", identity);
        var cut = Render<Shared.RequestSessions>(p => p
            .Add(c => c.Request, Request)
            .Add(c => c.Readiness, readiness));
        cut.WaitForAssertion(() => cut.Find("[data-abom-request-sessions]"));
        return cut;
    }

    /// <summary>
    /// Before anybody starts one, the row says what a session may do, since what it may not is the
    /// part a reader needs before pressing it.
    /// </summary>
    [Fact]
    public void ARequestNoAgentHasWorkedOffersOneAndSaysWhatItMayTouch()
    {
        var cut = Render(NoSessions);

        cut.WaitForAssertion(() => Assert.False(cut.Find("[data-abom-session-start]").HasAttribute("disabled")));
        var empty = cut.Find("[data-abom-sessions-empty]").TextContent;
        Assert.Contains("only the query", empty);
        Assert.Contains("expected results", empty);
        Assert.DoesNotContain("accepted", empty);
    }

    /// <summary>A session records who started it, so nobody signed out can start one.</summary>
    [Fact]
    public void NobodySignedOutCanStartASession()
    {
        var cut = Render(NoSessions, identity: SignedOut);

        cut.WaitForAssertion(() => Assert.True(cut.Find("[data-abom-session-start]").HasAttribute("disabled")));
    }

    /// <summary>
    /// A session the server would refuse is not offered: the button says what is missing rather
    /// than failing after it is pressed.
    /// </summary>
    [Fact]
    public void ASessionIsNotOfferedWhileSomethingItNeedsIsMissing()
    {
        var cut = Render(NoSessions, readiness: new Models.AuthoringReadiness
        {
            Known = true, Ready = false,
            Checks =
            [
                new Models.ReadinessCheck { Key = "codeql", Name = "CodeQL CLI", State = "ready" },
                new Models.ReadinessCheck
                {
                    Key = "copilot", Name = "Copilot CLI", State = "problem", Reason = "copilot is not on PATH",
                },
            ],
        });

        cut.WaitForAssertion(() => Assert.True(cut.Find("[data-abom-session-start]").HasAttribute("disabled")));
        var blocked = cut.Find("[data-abom-session-blocked]").TextContent;
        Assert.Contains("Copilot CLI", blocked);
        Assert.Contains("copilot is not on PATH", blocked);
    }

    /// <summary>A server that cannot say what a session needs leaves the decision to its refusal.</summary>
    [Fact]
    public void AnUnknownReadinessDoesNotHoldASessionBack()
    {
        var cut = Render(NoSessions, readiness: new Models.AuthoringReadiness { Known = false });

        cut.WaitForAssertion(() => Assert.False(cut.Find("[data-abom-session-start]").HasAttribute("disabled")));
        Assert.Empty(cut.FindAll("[data-abom-session-blocked]"));
    }

    [Fact]
    public void StartingASessionAsksTheServerAsWhoeverIsSignedIn()
    {
        var cut = Render(NoSessions);
        cut.WaitForAssertion(() => Assert.False(cut.Find("[data-abom-session-start]").HasAttribute("disabled")));

        cut.Find("[data-abom-session-start]").Click();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Method == "POST"
            && r.Url.EndsWith("api/query-requests/7/sessions", StringComparison.Ordinal)
            && r.Body.Contains("\"by\":\"operator\"", StringComparison.Ordinal)));
    }

    /// <summary>
    /// ql-mcp runs CodeQL outside ABOM's own runner, and the only way a reader sees it working is
    /// what the gate saw approved and not yet answered.
    /// </summary>
    [Fact]
    public void ARunningSessionSaysWhatCodeQLIsDoingAndCanBeStopped()
    {
        Answer("api/query-requests/7/sessions/stop", Finished);
        var cut = Render(Running);

        cut.WaitForAssertion(() => cut.Find("[data-abom-session-stop]"));
        Assert.Empty(cut.FindAll("[data-abom-session-start]"));
        var flight = cut.Find("[data-abom-session-inflight]").TextContent;
        Assert.Contains("codeql_test_run", flight);
        Assert.Contains("test/CheckoutTagPin", flight);

        cut.Find("[data-abom-session-stop]").Click();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Method == "POST"
            && r.Url.EndsWith("api/query-requests/7/sessions/stop", StringComparison.Ordinal)));
    }

    // Every call the agent makes is followed now, not only CodeQL's, so the line names the tool
    // rather than saying CodeQL is running a file read.
    [Fact]
    public void ACallInFlightIsNamedByItsToolRatherThanAsCodeQL()
    {
        var cut = Render(Running.Replace("ql-mcp-codeql_test_run", "view")
            .Replace("\"subject\":\"test/CheckoutTagPin\"", "\"subject\":\"src/CheckoutTagPin.ql\""));

        var flight = cut.WaitForElement("[data-abom-session-inflight]").TextContent;
        Assert.Contains("view", flight);
        Assert.Contains("src/CheckoutTagPin.ql", flight);
        Assert.DoesNotContain("CodeQL", flight);
    }

    // A first read that fails is no reason to stop reading: unarmed, the page stayed on "cannot
    // load" until somebody changed how often pages refresh.
    [Fact]
    public void AFirstReadThatFailsIsTriedAgainAndNotReadAsNoSessions()
    {
        Fail("api/query-requests/7/sessions", System.Net.HttpStatusCode.ServiceUnavailable);
        var cut = Render(NoSessions);

        cut.WaitForAssertion(() => Assert.Contains("Cannot load sessions", cut.Markup));
        Assert.Empty(cut.FindAll("[data-abom-sessions-empty]"));
        Assert.Equal(TimeSpan.FromSeconds(Abom.WebAssembly.Services.FollowService.DefaultSeconds), cut.Instance.Beat);
    }

    // A re-read that fails is not evidence the sessions went: what was read stays, said to be from
    // before, and the warning goes once a read succeeds.
    [Fact]
    public void AReReadThatFailsKeepsTheSessionsShownUntilOneSucceeds()
    {
        var cut = Render(Finished);
        cut.WaitForAssertion(() => cut.Find("[data-abom-session='1']"));
        Fail("api/query-requests/7/sessions", System.Net.HttpStatusCode.ServiceUnavailable);

        cut.Render(p => p.Add(c => c.Started, 1));

        cut.WaitForAssertion(() => Assert.Contains("Cannot reload sessions", cut.Markup));
        Assert.NotEmpty(cut.FindAll("[data-abom-session='1']"));

        Recover("api/query-requests/7/sessions");
        cut.Render(p => p.Add(c => c.Started, 2));

        cut.WaitForAssertion(() => Assert.DoesNotContain("Cannot reload sessions", cut.Markup));
        Assert.NotEmpty(cut.FindAll("[data-abom-session='1']"));
    }

    // Why a session could not start is the server's answer to somebody pressing Start, and reading
    // the list again does not answer it.
    [Fact]
    public void ARefusalToStartOutlivesTheReadThatFollowsIt()
    {
        RefuseWrite("api/query-requests/7/sessions", System.Net.HttpStatusCode.Conflict, "discovery holds CodeQL");
        var cut = Render(NoSessions);

        cut.WaitForElement("[data-abom-session-start]").Click();

        cut.WaitForAssertion(() => Assert.Contains("discovery holds CodeQL",
            cut.Find("[data-abom-session-refusal]").TextContent));
        Assert.True(Sent.Count(r => r.Method == "GET" && r.Url.EndsWith("api/query-requests/7/sessions", StringComparison.Ordinal)) >= 2,
            "the sessions were not read again after the refusal");
    }

    /// <summary>The refusals are the security record, so each is shown with the reason the agent was given.</summary>
    [Fact]
    public void ASessionsTimelineShowsWhatWasRefusedAndWhy()
    {
        Answer("api/authoring-events", Timeline);
        var cut = Render(Finished);

        cut.WaitForAssertion(() => cut.Find("[data-abom-session='1']"));
        var row = cut.Find("[data-abom-session='1']").TextContent;
        Assert.Contains("4 refused", row);

        cut.Find("[data-abom-session-timeline]").Click();

        cut.WaitForAssertion(() => Assert.Equal(2, cut.FindAll("[data-abom-event]").Count));
        var refused = cut.Find("[data-abom-event='37']").TextContent;
        Assert.Contains("rm -rf ~", refused);
        Assert.Contains("not part of the authoring loop", refused);
        Assert.Contains("3356 ms", cut.Find("[data-abom-event='43']").TextContent);
    }

    /// <summary>
    /// A call that failed is read for why it failed: a row saying only "failed" left a reader, like
    /// the agent, guessing at a refusal whose reason was on record.
    /// </summary>
    [Fact]
    public void AFailedCallSaysWhyItFailed()
    {
        Answer("api/authoring-events", """
            {"items":[{"rowId":18,"sessionId":1,"at":"2026-09-24T04:05:55Z","hook":"post-tool-use-failure",
              "tool":"abom_target","subject":"","decision":"failed","step":"target","reason":"",
              "detail":"this session is already about src/CheckoutTagPin.ql; work on it","tookMs":null}],
             "total":1,"through":18}
            """);
        var cut = Render(Finished);
        cut.WaitForAssertion(() => cut.Find("[data-abom-session='1']"));

        cut.Find("[data-abom-session-timeline]").Click();

        cut.WaitForAssertion(() => Assert.Contains("already about src/CheckoutTagPin.ql",
            cut.Find("[data-abom-event='18']").TextContent));
    }

    /// <summary>
    /// The tree the agent read the fixtures as is shipped only when a reader asks for it: a listing
    /// carrying every tree would send megabytes to draw a row.
    /// </summary>
    [Fact]
    public void APrintedTreeIsShownWhenAskedFor()
    {
        Answer("api/authoring-asts/3", Tree);
        var cut = Render(Finished, Trees);

        cut.WaitForAssertion(() => cut.Find("[data-abom-session-tab='trees']"));
        Assert.DoesNotContain(Sent, r => r.Url.Contains("api/authoring-asts/3", StringComparison.Ordinal));
        cut.Find("[data-abom-session-tab='trees']").Click();
        Assert.Contains("test/CheckoutTagPin/CheckoutTagPin.testproj", cut.Find("[data-abom-ast='3']").TextContent);
        Assert.Empty(cut.FindAll("[data-abom-ast-body]"));
        Assert.DoesNotContain(Sent, r => r.Url.Contains("api/authoring-asts/3", StringComparison.Ordinal));

        cut.Find("[data-abom-ast-show]").Click();

        cut.WaitForAssertion(() => Assert.Contains("UsesStep", cut.Find("[data-abom-ast-body]").TextContent));
    }

    private const string Spent = """
        {"sessions":[{"rowId":1,"requestId":7,"state":"stopped","startedBy":"operator",
          "model":"claude-opus-5","startedAt":"2026-09-24T02:20:25Z","endedAt":"2026-09-24T02:22:31Z",
          "detail":"stopped by an operator","events":30,"refused":0,
          "modelCalls":14,"inputTokens":120345,"outputTokens":8012,"nanoAiu":1250000000}],
         "inFlight":[]}
        """;

    /// <summary>
    /// A session stopped part way still cost something, and a cost the CLI never reported is not a
    /// session that cost nothing.
    /// </summary>
    [Fact]
    public void ASessionSaysWhatItSpentAndNeverReadsAnUnreportedCostAsZero()
    {
        var cut = Render(Spent);
        cut.WaitForAssertion(() => cut.Find("[data-abom-session-spend]"));
        var spend = cut.Find("[data-abom-session-spend]").TextContent;
        Assert.Contains("1.25 AI units", spend);
        Assert.Contains("14 model calls", spend);

        var unreported = Render(Finished);
        unreported.WaitForAssertion(() => unreported.Find("[data-abom-session-spend]"));
        var text = unreported.Find("[data-abom-session-spend]").TextContent;
        Assert.Contains("Not reported", text);
        Assert.DoesNotContain("0 AI units", text);
    }

    /// <summary>
    /// The agent's own words are what a reader asked for least: they stay folded until asked for.
    /// </summary>
    [Fact]
    public void WhatTheAgentSaidIsFoldedUntilAskedFor()
    {
        var cut = Render(Finished);
        cut.WaitForAssertion(() => cut.Find("[data-abom-session-said]"));
        Assert.Empty(cut.FindAll("[data-abom-session-detail]"));

        cut.Find("[data-abom-session-said]").Click();

        cut.WaitForAssertion(() => Assert.Contains("Stopped once the tests passed.",
            cut.Find("[data-abom-session-detail]").TextContent));
    }

    /// <summary>
    /// The expected results are the agent's to write before the query, so nothing here makes the
    /// query's own output the expected result, and nothing asks the server to.
    /// </summary>
    [Fact]
    public void NothingOffersToAcceptTheQuerysOutput()
    {
        var cut = Render(Finished);
        cut.WaitForAssertion(() => cut.Find("[data-abom-session='1']"));

        Assert.DoesNotContain("Accept", cut.Markup, StringComparison.Ordinal);
        Assert.DoesNotContain(Sent, r => r.Url.Contains("/review", StringComparison.Ordinal));
    }
}
