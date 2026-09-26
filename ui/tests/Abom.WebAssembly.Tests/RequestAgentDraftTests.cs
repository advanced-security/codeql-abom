using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A request asked from a run can be drafted by an agent from what was picked; the draft fills the
/// form only when the operator chooses it, and saving is still theirs.
/// </summary>
public class RequestAgentDraftTests : AbomTestContext
{
    private const string SignedIn = """{"authenticated":true,"login":"operator","accounts":[]}""";

    private static readonly Models.QueryRequest Asked = new()
    {
        SourceRunId = 9, QueryId = "acme/pin", Kind = "false-alarm",
        Evidence = [new Models.RequestEvidence { RunId = 9, Repository = "octo/app", FindingRowId = 41, Kind = "false-alarm" }],
    };

    private const string Drafted = """
        {"drafts":[{"rowId":5,"runId":9,"queryId":"acme/pin","state":"finished","startedBy":"operator",
          "startedAt":"2026-09-24T03:00:00Z","detail":"Drafted a request.","events":6,
          "draft":{"rowId":2,"sessionId":5,"draftedAt":"2026-09-24T03:01:00Z",
            "description":"stop reporting checkout pinned to a full SHA","compliant":"uses: a@sha",
            "nonCompliant":"uses: a@v4","rationale":"the picked finding is a full SHA pin"}}]}
        """;

    private IRenderedComponent<Shared.RequestAgentDraft> Render(Action<Models.AgentDraft>? used = null)
    {
        Answer("api/accounts", SignedIn);
        var cut = Render<Shared.RequestAgentDraft>(p => p
            .Add(c => c.Asked, Asked)
            .Add(c => c.QueryId, "acme/pin")
            .Add(c => c.OnUse, (Models.AgentDraft d) => used?.Invoke(d)));
        cut.WaitForAssertion(() => Assert.False(cut.Find("[data-abom-draft-start]").HasAttribute("disabled")));
        return cut;
    }

    [Fact]
    public void DraftingIsAskedOfTheServerWithWhatWasPicked()
    {
        Answer("api/runs/9/drafts", Drafted);
        var cut = Render();

        cut.Find("[data-abom-draft-start]").Click();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Method == "POST"
            && r.Url.EndsWith("api/runs/9/drafts", StringComparison.Ordinal)
            && r.Body.Contains("\"queryId\":\"acme/pin\"", StringComparison.Ordinal)
            && r.Body.Contains("\"by\":\"operator\"", StringComparison.Ordinal)
            && r.Body.Contains("\"findingRowId\":41", StringComparison.Ordinal)));
    }

    /// <summary>A draft fills nothing by arriving: the operator chooses to use it, then saves.</summary>
    [Fact]
    public void ADraftIsOfferedAndUsedOnlyWhenChosen()
    {
        Answer("api/runs/9/drafts", Drafted);
        Models.AgentDraft? used = null;
        var cut = Render(d => used = d);

        cut.Find("[data-abom-draft-start]").Click();
        cut.WaitForAssertion(() => cut.Find("[data-abom-draft-offer]"));
        Assert.Contains("stop reporting checkout pinned to a full SHA", cut.Find("[data-abom-draft-offer]").TextContent);
        Assert.Null(used);

        cut.Find("[data-abom-draft-use]").Click();

        Assert.NotNull(used);
        Assert.Equal("uses: a@v4", used!.NonCompliant);
    }
}
