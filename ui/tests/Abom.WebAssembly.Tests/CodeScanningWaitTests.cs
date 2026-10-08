using System.Text.Json;
using Abom.WebAssembly.Models;
using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A push held for code scanning's own analysis is queued and deliberately not claimed yet. Drawn as
/// queued it reads as work about to run, and as a retry it reads as a failure; it is neither, so it
/// is a state of its own wherever the queue's states are read.
/// </summary>
public class CodeScanningWaitTests : AbomTestContext
{
    private const string Held = """
        [{"nodeKey":"octo/app","ref":"","owner":"octo","repo":"app",
          "status":"pending","depth":0,"score":1,"attempts":0,"analysisCount":1,
          "resolvedSha":"a1","discoveredFrom":"","reason":"ref moved to c2",
          "claimedAt":"","nextAttemptAt":"2026-10-07T12:10:00Z","supplyChain":"runtime","analyzedAt":"",
          "tier":"owned","awaitingCodeScanning":"c2"}]
        """;

    // The row is pending to the queue, and waiting to a reader.
    [Fact]
    public void AHeldRowReadsAsWaitingNotQueued()
    {
        var rows = JsonSerializer.Deserialize<List<FrontierItem>>(Held,
            new JsonSerializerOptions(JsonSerializerDefaults.Web))!;

        Assert.Equal("waiting", rows[0].State);
    }

    // The bar draws every state the server counts; a held row left out would make it sum to less.
    [Fact]
    public void TheQueueBarDrawsWorkWaitingForCodeScanning()
    {
        var state = JsonSerializer.Deserialize<DiscoveryState>(
            DiscoveryStateBuilder.Running().WithWaiting(2).ToJson(),
            new JsonSerializerOptions(JsonSerializerDefaults.Web))!;

        var bar = Render<Shared.QueueBar>(p => p.Add(x => x.State, state));

        var label = bar.Find(".abom-queue-bar").GetAttribute("aria-label") ?? "";
        Assert.Contains("2 waiting for code scanning", label);
        Assert.Equal(state.Count("queued") + state.Count("running") + 2, state.Unresolved);
    }

    // Held work can be listed on its own, with when its wait ends.
    [Fact]
    public void WorkWaitingForCodeScanningCanBeListedOnItsOwn()
    {
        Answer("api/discovery/suites", """{"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}""");
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().WithWaiting(1).ToJson());
        Answer("api/frontier", Held);
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/actions", """{"items":[],"total":0}""");

        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-unit='coverage']")));
        page.Find("[data-abom-unit='coverage']").Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-coverage-version]")));

        var chip = page.Find("[data-abom-coverage-state='waiting']");
        Assert.Contains("Waiting for code scanning", chip.TextContent);
        chip.Click();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("status=waiting")));
    }

    // A held push is neither asked for by name nor about to run, so the objective says what it waits on.
    [Fact]
    public void TheObjectiveSaysWhatAHeldPushWaitsFor()
    {
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().ToJson());
        Answer("api/discovery/objective", """
            {"progress":[
               {"depth":0,"total":3,"done":2,"settled":2,"pending":1,"running":0,"available":0,"refresh":1}],
             "working":0,"hasOutstanding":false,"maxDepth":5,
             "stages":{"next":"","waiting":1},"recent":[],
             "totals":{"cycles":0,"millis":0,"fetches":0,"builds":0,"evaluations":0,"probes":0,
                       "analyzed":0,"discovered":0}}
            """);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-objective-stage]")));
        var stage = cut.Find("[data-abom-objective-stage]").TextContent;
        Assert.Contains("1 push waits for code scanning's own analysis of it", stage);
    }
}
