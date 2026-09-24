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
          "detail":"Acceptance is the operator's call; nothing was accepted here.","events":84,"refused":4}],
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
        {"events":[
          {"rowId":37,"sessionId":1,"at":"2026-09-24T02:21:00Z","hook":"pre-tool-use","tool":"bash",
           "subject":"rm -rf ~","decision":"refused","step":"",
           "reason":"bash is not part of the authoring loop","detail":"","tookMs":null},
          {"rowId":43,"sessionId":1,"at":"2026-09-24T02:21:08Z","hook":"post-tool-use",
           "tool":"ql-mcp-codeql_test_extract","subject":"test/CheckoutTagPin","decision":"succeeded",
           "step":"database","reason":"","detail":"","tookMs":3356}],
         "next":43}
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
        string identity = SignedIn)
    {
        Answer("api/query-requests/7/sessions", sessions);
        Answer("api/query-requests/7/asts", trees);
        Answer("api/accounts", identity);
        var cut = Render<Shared.RequestSessions>(p => p.Add(c => c.Request, Request));
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
        Assert.Contains("accepted", empty);
    }

    /// <summary>A session records who started it, so nobody signed out can start one.</summary>
    [Fact]
    public void NobodySignedOutCanStartASession()
    {
        var cut = Render(NoSessions, identity: SignedOut);

        cut.WaitForAssertion(() => Assert.True(cut.Find("[data-abom-session-start]").HasAttribute("disabled")));
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

    /// <summary>The refusals are the security record, so each is shown with the reason the agent was given.</summary>
    [Fact]
    public void ASessionsTimelineShowsWhatWasRefusedAndWhy()
    {
        Answer("api/authoring-sessions/1/events", Timeline);
        var cut = Render(Finished);

        cut.WaitForAssertion(() => cut.Find("[data-abom-session='1']"));
        var row = cut.Find("[data-abom-session='1']").TextContent;
        Assert.Contains("4 refused", row);
        Assert.Contains("nothing was accepted here", row);

        cut.Find("[data-abom-session-timeline]").Click();

        cut.WaitForAssertion(() => Assert.Equal(2, cut.FindAll("[data-abom-event]").Count));
        var refused = cut.Find("[data-abom-event='37']").TextContent;
        Assert.Contains("rm -rf ~", refused);
        Assert.Contains("not part of the authoring loop", refused);
        Assert.Contains("3.4 s", cut.Find("[data-abom-event='43']").TextContent);
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

        cut.WaitForAssertion(() => cut.Find("[data-abom-ast='3']"));
        Assert.Contains("test/CheckoutTagPin/CheckoutTagPin.testproj", cut.Find("[data-abom-ast='3']").TextContent);
        Assert.Empty(cut.FindAll("[data-abom-ast-body]"));
        Assert.DoesNotContain(Sent, r => r.Url.Contains("api/authoring-asts/3", StringComparison.Ordinal));

        cut.Find("[data-abom-ast-show]").Click();

        cut.WaitForAssertion(() => Assert.Contains("UsesStep", cut.Find("[data-abom-ast-body]").TextContent));
    }
}
