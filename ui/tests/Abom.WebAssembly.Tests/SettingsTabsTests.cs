using Bunit;
using Microsoft.AspNetCore.Components;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Settings is in tabs, one in view at a time, addressed by <c>?tab=</c> so a link and a reload
/// land where they were. Links written before the tabs, by anchor, still land on the section.
/// </summary>
public class SettingsTabsTests : AbomTestContext
{
    private NavigationManager Nav => Services.GetRequiredService<NavigationManager>();

    private static string? Selected(IRenderedComponent<Pages.Settings> cut) =>
        cut.FindAll("[data-abom-tab]").SingleOrDefault(t => t.GetAttribute("aria-selected") == "true")
            ?.GetAttribute("data-abom-tab");

    private static bool Shown(IRenderedComponent<Pages.Settings> cut, string panel) =>
        cut.FindAll($"[data-abom-settings-panel='{panel}']") is { Count: 1 } found && !found[0].HasAttribute("hidden");

    // Discovery is what you come back to most, so a bare address opens it.
    [Fact]
    public void SettingsOpensOnDiscovery()
    {
        var cut = Render<Pages.Settings>();

        Assert.Equal("discovery", Selected(cut));
        Assert.True(Shown(cut, "discovery"));
        Assert.False(Shown(cut, "accounts"));
        Assert.Empty(cut.FindAll("[data-abom-settings-panel='data']"));
    }

    [Theory]
    [InlineData("accounts")]
    [InlineData("code-scanning")]
    [InlineData("agent-sessions")]
    [InlineData("data")]
    [InlineData("you")]
    public void ATabInTheAddressIsTheOneInView(string tab)
    {
        var cut = RenderSettings(tab);

        Assert.Equal(tab, Selected(cut));
        Assert.True(Shown(cut, tab));
        Assert.Single(cut.FindAll("[data-abom-settings-panel]:not([hidden])"));
    }

    [Fact]
    public void AnUnknownTabOpensDiscovery()
    {
        var cut = RenderSettings("nonsense");

        Assert.Equal("discovery", Selected(cut));
    }

    // Links from before the tabs named a section by anchor; each still lands on its tab.
    [Theory]
    [InlineData("declared-accounts", "accounts")]
    [InlineData("agent-tools", "agent-sessions")]
    [InlineData("you", "you")]
    public void AnAnchorFromBeforeTheTabsOpensItsTab(string anchor, string tab)
    {
        Nav.NavigateTo("/settings#" + anchor);

        var cut = Render<Pages.Settings>();

        Assert.Equal(tab, Selected(cut));
    }

    // Choosing a tab puts it in the address, replacing the entry, so Back leaves Settings rather
    // than stepping through every tab looked at.
    [Fact]
    public void ChoosingATabPutsItInTheAddress()
    {
        var cut = Render<Pages.Settings>();

        cut.Find("[data-abom-tab='data']").Click();

        Assert.EndsWith("/settings?tab=data", Nav.Uri);
        Assert.Equal("data", Selected(cut));
        Assert.True(Shown(cut, "data"));
    }

    // Every tab says what it holds before it is pressed.
    [Fact]
    public void EveryTabSaysWhatItHolds()
    {
        var cut = Render<Pages.Settings>();

        var tabs = cut.FindAll("[data-abom-tab]");
        Assert.Equal(6, tabs.Count);
        Assert.All(tabs, t => Assert.False(string.IsNullOrWhiteSpace(t.Closest("[data-abom-tip]")?.GetAttribute("data-abom-tip"))));
    }

    private const string Unsigned = """
        {"state":"unsigned","reason":"no recorded decision shows that saving it was allowed","digest":"","decidedAt":"","decidedBy":""}
        """;

    private const string NothingEnumerated = """
        {"progress":[],"working":0,"hasOutstanding":false,"maxDepth":5,
         "recent":[],"totals":{"cycles":0,"millis":0,"fetches":0,"builds":0,
                               "evaluations":0,"probes":0,"analyzed":0,"discovered":0}}
        """;

    // Until the declaration is confirmed nothing counts as yours, whichever tab is open; Accounts,
    // where it is confirmed, says so with the Confirm beside it instead.
    [Theory]
    [InlineData("discovery", true)]
    [InlineData("code-scanning", true)]
    [InlineData("data", true)]
    [InlineData("you", true)]
    [InlineData("accounts", false)]
    public void AnUnconfirmedDeclarationIsSaidAboveEveryTabButAccounts(string tab, bool above)
    {
        Answer("api/owned-scope/decision", Unsigned);

        var cut = RenderSettings(tab);

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.EndsWith("api/owned-scope/decision", StringComparison.Ordinal)));
        cut.WaitForAssertion(() => Assert.Equal(above ? 1 : 0, cut.FindAll("[data-abom-declaration-state='unsigned']").Count));
    }

    // Measured on has-ghas: "Read 32 of 0 declared repositories", the 32 kept from before the
    // declaration lost its decision, and 0 because nothing counts until it is confirmed.
    [Fact]
    public void CodeScanningDoesNotCountReadingsAgainstAnUnconfirmedDeclaration()
    {
        Answer("api/owned-scope/decision", Unsigned);
        Answer("api/code-scanning/readings", """{"declared":0,"read":32,"oldestRead":"2026-11-18T21:04:00Z","counts":{}}""");

        var cut = RenderSettings("code-scanning");

        cut.WaitForAssertion(() => Assert.Contains("not confirmed", cut.Find("[data-abom-setup-readings]").TextContent));
        Assert.DoesNotContain("of 0 declared", cut.Find("[data-abom-setup-readings]").TextContent);
    }

    // The crawl has nothing outstanding because nothing is yours yet, not because nothing was found.
    [Fact]
    public void TheCrawlSaysItWaitsOnTheDeclarationRatherThanThatNothingWasEnumerated()
    {
        Answer("api/owned-scope/decision", Unsigned);
        Answer("api/discovery/objective", NothingEnumerated);
        Answer("api/owned-scope", """[{"owner":"has-ghas","kind":"organization","enabled":true}]""");

        var cut = RenderSettings("discovery");

        cut.WaitForAssertion(() => Assert.Contains("not confirmed", cut.Find("[data-abom-objective-empty]").TextContent));
        Assert.DoesNotContain("Nothing has been enumerated yet", cut.Markup);
        cut.WaitForAssertion(() => Assert.Contains("not confirmed", cut.Find("[data-abom-enumeration]").TextContent));
        Assert.DoesNotContain("enumerated under it", cut.Find("[data-abom-enumeration]").TextContent);
    }

    // Re-analyzing what you own has nothing to act on until the declaration is confirmed, so the
    // button says so and is refused with the reason, rather than offering to re-analyze none.
    [Fact]
    public void ReanalyzingWhatYouOwnWaitsOnTheDeclaration()
    {
        Answer("api/owned-scope/decision", Unsigned);
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().ToJson());
        Answer("api/discovery/suites", """
            {"available":[],"verified":true,"tiers":{"owned":[],"direct":[],"transitive":[]},"counts":{}}
            """);
        Answer("api/frontier", "[]");
        Answer("api/owned-scope", """[{"owner":"has-ghas","kind":"organization","enabled":true}]""");

        var cut = RenderSettings("discovery");

        cut.WaitForAssertion(() =>
        {
            var owned = cut.FindAll(".abom-requeue-tier").Single(b => b.TextContent.Trim().StartsWith("owned"));
            Assert.Equal("owned (not confirmed)", owned.TextContent.Trim());
            Assert.True(owned.HasAttribute("disabled"));
            Assert.Contains("not confirmed", owned.ParentElement!.OuterHtml.Replace(owned.OuterHtml, ""));
        });
        Assert.DoesNotContain("owned (0)", cut.Markup);
    }

    private static string? Badge(IRenderedComponent<Pages.Settings> cut, string tab) =>
        cut.FindAll($"[data-abom-tab='{tab}'] [data-abom-tab-badge]").SingleOrDefault()?.TextContent.Trim();

    // A tab that needs you says so in a word on the tab, so it is found without opening each.
    [Fact]
    public void AccountsIsMarkedWhileTheDeclarationWaitsToBeConfirmed()
    {
        Answer("api/owned-scope/decision", Unsigned);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Equal("confirm", Badge(cut, "accounts")));
        Assert.Null(Badge(cut, "discovery"));
    }

    [Fact]
    public void AccountsIsMarkedWhileAnEditIsUnsaved()
    {
        Answer("api/owned-scope/decision", """{"state":"none","reason":"","digest":"","decidedAt":"","decidedBy":""}""");
        Answer("api/owned-scope", "[]");
        Answer("api/accounts", """{"authenticated":true,"login":"octocat","accounts":[]}""");
        var cut = RenderSettings("accounts");
        cut.WaitForElement("[data-abom-new-owner] input").Change("private-org");
        cut.Find("[data-abom-declare-typed]").Click();

        cut.WaitForAssertion(() => Assert.Equal("unsaved", Badge(cut, "accounts")));
    }

    [Fact]
    public void AgentSessionsIsMarkedWhileACheckFails()
    {
        Answer("api/authoring/readiness", """
            {"known":true,"ready":false,"checks":[{"key":"copilot","name":"Copilot CLI","state":"problem","path":"","reason":"no Copilot CLI is named"}]}
            """);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Equal("problem", Badge(cut, "agent-sessions")));
    }

    [Fact]
    public void NoTabIsMarkedWhenNothingNeedsYou()
    {
        Answer("api/owned-scope/decision", """{"state":"signed","reason":"","digest":"ab","decidedAt":"","decidedBy":"octocat"}""");
        Answer("api/authoring/readiness", """{"known":true,"ready":true,"checks":[]}""");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.EndsWith("api/authoring/readiness", StringComparison.Ordinal)));
        Assert.Empty(cut.FindAll("[data-abom-tab-badge]"));
    }
}
