using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A control the signed-in role does not allow is offered disabled, naming the role it needs, on a
/// tip outside the control: offered and then refused on press, it reads as broken.
/// </summary>
public sealed class RoleRefusalTests : AbomTestContext
{
    private const string Viewer = """
        {"mode":"signed-in","user":{"id":"https://idp.example#s","name":"Grace Hopper","username":"","role":"viewer"},"may":[],
         "needs":{"abom/export-query":"operator","abom/prune":"operator","abom/declare-scope":"operator",
                  "abom/discovery-control":"operator"}}
        """;

    private static void SaysTheRoleItNeeds(AngleSharp.Dom.IElement control)
    {
        Assert.True(control.HasAttribute("disabled"));
        Assert.Contains("needs the operator role", control.Closest("[data-abom-tip]")!.GetAttribute("data-abom-tip"));
    }

    [Fact]
    public void AnExportTheRoleDoesNotAllowIsDisabledNamingTheRole()
    {
        Answer("api/me", Viewer);
        Answer("api/queries/components?", """
            {"query":"acme/a","version":"v2","pack":"abom/custom-queries","packVersion":"2.27.0","dependencies":[],
             "outside":[],"development":true,"deployed":false,"bundleSha256":"ab","missing":[],"newestTest":null,
             "components":[{"role":"query","path":"src/A.ql","sha256":"bb","size":3,"bundled":true}],
             "exportable":true,"exportTarget":"acme/queries","exportBlocked":""}
            """);

        var panel = Render<Shared.QueryComponents>(p => p.Add(c => c.QueryId, "acme/a"));

        panel.WaitForAssertion(() => SaysTheRoleItNeeds(panel.Find("[data-abom-component-export-blocked]")));
    }

    [Fact]
    public void APruneTheRoleDoesNotAllowIsDisabledNamingTheRole()
    {
        Answer("api/me", Viewer);
        Answer("api/owned-scope", """[{"owner":"octo-org","kind":"organization","enabled":true}]""");
        Answer("api/prune", """
            {"declared":2,"reachable":10,"removed":{"repositories":3,"nodes":40,"edges":12,"queued":0},
             "tombstoned":5,"applied":false,"unavailable":"","live":{"nodes":400,"edges":300},
             "majority":false,"proof":"P1","checkouts":[]}
            """);

        var control = Render<Shared.PruneControl>();

        control.WaitForAssertion(() =>
        {
            SaysTheRoleItNeeds(control.Find("[data-abom-prune-review]"));
            SaysTheRoleItNeeds(control.Find("[data-abom-prune-compact]"));
        });
    }

    [Fact]
    public void ADeclarationTheRoleMayNotDecideIsDisabledNamingTheRole()
    {
        Answer("api/me", Viewer);
        Answer("api/owned-scope/decision", """{"state":"unsigned","reason":"it names no decision","digest":"","decidedAt":"","decidedBy":""}""");
        Answer("api/owned-scope", """[{"owner":"acme","kind":"organization","enabled":true,"includesOnly":false,"includes":[],"declaredAt":"","declaredBy":"cli","state":"confirmed"}]""");
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", """{"authenticated":true,"login":"grace","host":"github.com","verifiedAt":"","problem":null,"canReadMemberships":true,"accounts":[]}""");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() =>
        {
            SaysTheRoleItNeeds(cut.Find("[data-abom-confirm-scope]"));
            SaysTheRoleItNeeds(cut.Find("[data-abom-save]"));
        });
    }

    // Where a person meets it: on Settings, below the declaration, after everything else there loads.
    [Fact]
    public void AnalysisOnSettingsIsDisabledForARoleThatMayNotStartIt()
    {
        Answer("api/me", Viewer);
        Answer("api/discovery/status", DiscoveryStateBuilder.NeverStarted().WithOwned(198, 0).ToJson());
        Answer("api/frontier", "[]");
        Answer("api/owned-scope/decision", """{"state":"signed","reason":"","digest":"ab","decidedAt":"","decidedBy":"","judgedAs":""}""");
        Answer("api/owned-scope", """[{"owner":"acme","kind":"organization","enabled":true,"includesOnly":false,"includes":[],"declaredAt":"","declaredBy":"cli","state":"confirmed"}]""");
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", """{"authenticated":true,"login":"grace","host":"github.com","verifiedAt":"","problem":null,"canReadMemberships":true,"accounts":[]}""");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => SaysTheRoleItNeeds(cut.Find("[data-abom-run]")));
    }

    // The role is known long before the rest of the controls' reads come back, and the control is
    // never offered in between: pressed then, it would be refused by the server instead.
    [Fact]
    public void AnalysisIsRefusedBeforeTheRestOfTheControlsLoad()
    {
        Answer("api/me", Viewer);
        Answer("api/discovery/status", DiscoveryStateBuilder.NeverStarted().WithOwned(198, 0).ToJson());
        Answer("api/frontier", "[]");
        Answer("api/owned-scope", """[{"owner":"octo-org"}]""");
        var me = Withhold("api/me");
        var release = Withhold("api/discovery/status");
        var cut = Render<Shared.DiscoveryControls>();

        me();

        cut.WaitForAssertion(() => SaysTheRoleItNeeds(cut.Find("[data-abom-run]")));
        release();
    }

    [Fact]
    public void AnalysisTheRoleMayNotStartIsDisabledNamingTheRole()
    {
        Answer("api/me", Viewer);
        Answer("api/discovery/status", DiscoveryStateBuilder.NeverStarted().WithOwned(198, 0).ToJson());
        Answer("api/frontier", "[]");
        Answer("api/owned-scope", """[{"owner":"octo-org"}]""");

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => SaysTheRoleItNeeds(cut.Find("[data-abom-run]")));
    }

    // Every control on the panel is one act, discovery control, so the role that may not start
    // analysis may not re-analyze or change how it resumes either.
    [Fact]
    public void EveryDiscoveryControlTheRoleMayNotUseIsDisabledNamingTheRole()
    {
        Answer("api/me", Viewer);
        Answer("api/discovery/status", DiscoveryStateBuilder.NeverStarted().WithOwned(198, 0).ToJson());
        Answer("api/frontier", "[]");
        Answer("api/owned-scope", """[{"owner":"octo-org"}]""");

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() =>
        {
            var controls = cut.FindAll("button.abom-requeue");
            Assert.NotEmpty(controls);
            foreach (var control in controls)
            {
                SaysTheRoleItNeeds(control);
            }
            SaysTheRoleItNeeds(cut.Find("[data-abom-resume-on-start] input"));
        });
    }

    // A refusal the page did not foresee still says why, rather than ending the page.
    [Fact]
    public void AReanalysisTheServerRefusesSaysWhy()
    {
        Answer("api/discovery/status", DiscoveryStateBuilder.NeverStarted().WithOwned(198, 0).ToJson());
        Answer("api/frontier", "[]");
        Answer("api/owned-scope", """[{"owner":"octo-org"}]""");
        foreach (var tier in new[] { "owned", "direct", "transitive" })
        {
            RefuseWrite("api/discovery/requeue/" + tier, System.Net.HttpStatusCode.Forbidden,
                "re-analyzing needs the operator role");
        }

        var cut = Render<Shared.DiscoveryControls>();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll(".abom-requeue-tier")));
        cut.FindAll(".abom-requeue-tier")[0].Click();

        cut.WaitForAssertion(() => Assert.Contains("re-analyzing needs the operator role", cut.Markup));
    }

    private const string Operator = """
        {"mode":"signed-in","user":{"id":"https://idp.example#o","name":"Otto","username":"otto","role":"operator"},
         "may":["abom/discovery-control"],"needs":{"abom/discovery-control":"operator","abom/configure":"admin"}}
        """;

    // The control is given the refusal to disable itself by, and the tip says what it needs instead
    // of what it does.
    [Fact]
    public void AGuardedControlIsGivenTheRefusalAndItsTipSaysWhatIsNeeded()
    {
        Answer("api/me", Operator);

        var refused = Render<Shared.Guarded>(p => p
            .Add(g => g.Operation, "abom/configure").Add(g => g.Tip, "Save this pool's rate.")
            .Add(g => g.ChildContent, (string? refusal) => b => b.AddMarkupContent(0, $"<button disabled=\"{refusal is not null}\">Save</button>")));
        var allowed = Render<Shared.Guarded>(p => p
            .Add(g => g.Operation, "abom/discovery-control").Add(g => g.Tip, "Start the crawl.")
            .Add(g => g.ChildContent, (string? refusal) => b => b.AddMarkupContent(0, $"<button data-refused=\"{refusal is not null}\">Start</button>")));

        refused.WaitForAssertion(() => Assert.Contains("needs the admin role",
            refused.Find("[data-abom-tip]").GetAttribute("data-abom-tip")));
        Assert.Equal("Start the crawl.", allowed.Find("[data-abom-tip]").GetAttribute("data-abom-tip"));
        Assert.Equal("False", allowed.Find("button").GetAttribute("data-refused"));
    }

    // The bar's start button is on every page, so it is the first a viewer would press.
    [Fact]
    public void TheBarDoesNotOfferAViewerToStartTheCrawl()
    {
        Answer("api/me", Viewer);
        Answer("api/discovery/status", DiscoveryStateBuilder.NeverStarted().ToJson());

        var bar = Render<Shared.CrawlStatus>();

        bar.WaitForAssertion(() => SaysTheRoleItNeeds(bar.Find("[data-abom-crawl-toggle]")));
    }

    // Every component that asks for a change the role check can refuse offers it through Guarded, or
    // asks RoleService itself; one that does neither offers a control a viewer presses and is refused.
    [Fact]
    public void EveryControlThatChangesSomethingSaysWhichRoleItNeeds()
    {
        var root = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "../../../../.."));
        var writes = new System.Text.RegularExpressions.Regex(
            @"\bApi\.(Set|Save|Start|Stop|Delete|Advance|Print|Test|Cancel|Retry|Requeue|Convert|Forget|Verify|Approve|Act|Preview)[A-Za-z]*Async\(");
        var unguarded = Directory.EnumerateFiles(Path.Combine(root, "Shared"), "*.razor")
            .Concat(Directory.EnumerateFiles(Path.Combine(root, "Pages"), "*.razor"))
            .Where(f => writes.IsMatch(File.ReadAllText(f)))
            .Where(f => !File.ReadAllText(f).Contains("<Guarded") && !File.ReadAllText(f).Contains("RefusalAsync("))
            .Select(Path.GetFileName)
            .Order(StringComparer.Ordinal)
            .ToList();

        Assert.Empty(unguarded);
    }
}
