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
}
