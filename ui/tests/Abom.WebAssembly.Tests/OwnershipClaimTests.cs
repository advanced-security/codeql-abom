using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Who may declare, as the server answers it: a security manager whom memberships call a member,
/// a member's repositories they administer or maintain, the App's installations, and beside each
/// declaration whether the recorded role still supports it. The page renders the server's answers
/// and derives none of them.
/// </summary>
public class OwnershipClaimTests : AbomTestContext
{
    private const string Identity = """
        {"authenticated":true,"login":"data-douser","host":"github.com","identityBy":"gh",
         "verifiedAt":"2026-11-19T00:00:00Z","problem":null,"canReadMemberships":true,
         "accounts":[{"owner":"data-douser","kind":"user","role":"owner","canDeclare":true,"canInclude":false},
                     {"owner":"managed-org","kind":"organization","role":"member","securityManager":"yes",
                      "canDeclare":true,"canInclude":false},
                     {"owner":"member-org","kind":"organization","role":"member","securityManager":"no",
                      "canDeclare":false,"canInclude":true},
                     {"owner":"blind-org","kind":"organization","role":"member","securityManager":"unknown",
                      "securityManagerReason":"gh cannot read who manages security in blind-org without the admin:org scope (gh auth refresh -s admin:org)",
                      "canDeclare":false,"canInclude":true}]}
        """;

    private const string ByApp = """
        {"authenticated":true,"login":"abom-has-ghas[bot]","host":"github.com","identityBy":"app","readByApp":true,
         "verifiedAt":"2026-11-19T00:00:00Z","problem":null,"canReadMemberships":false,
         "accounts":[{"owner":"has-ghas","kind":"organization","role":"installation","canDeclare":true,"canInclude":false}]}
        """;

    // The live has-ghas shape: one account the App is installed on, one declared where it is not.
    private const string AppScope = """
        [{"owner":"has-ghas","kind":"organization","enabled":true,"includesOnly":false,"includes":[],
          "declaredAt":"2026-11-19T00:00:00Z","declaredBy":"interface","state":"confirmed",
          "installation":{"state":"installed","reason":"","installedBy":null},
          "check":{"state":"supported","role":"installation","detail":"the App is installed on has-ghas","includes":[]}},
         {"owner":"elsewhere","kind":"organization","enabled":true,"includesOnly":true,"includes":["tool"],
          "declaredAt":"2026-11-19T00:00:00Z","declaredBy":"interface","state":"unknown","notInstalled":["tool"],
          "installation":{"state":"unknown","reason":"not installed"},
          "check":{"state":"unknown","role":"","detail":"the App is not installed on elsewhere","includes":[]}}]
        """;

    private const string MemberRepositories = """
        {"owner":"member-org","problem":null,"repositories":[
          {"name":"maintained","permission":"maintain","declarable":true},
          {"name":"written","permission":"write","declarable":false}]}
        """;

    private const string Checked = """
        [{"owner":"member-org","kind":"organization","enabled":true,"includesOnly":true,
          "includes":["maintained","written"],"declaredAt":"2026-11-19T00:00:00Z","declaredBy":"interface",
          "state":"confirmed","notInstalled":["written"],
          "installation":{"state":"installed","reason":"","installedBy":"octocat"},
          "check":{"state":"unsupported","role":"member",
                   "detail":"an included repository is not one data-douser administers or maintains",
                   "includes":[{"repository":"maintained","state":"supported","role":"maintain","detail":"data-douser holds maintain"},
                               {"repository":"written","state":"unsupported","role":"write","detail":"data-douser holds write, which is not admin or maintain"}]}}]
        """;

    private const string NoImpact = """
        {"rootsNow":0,"rootsNext":0,"closureNow":0,"closureNext":0,
         "entering":[],"leaving":[],"enteringCount":0,"leavingCount":0,"truncated":false,
         "queuedNow":0,"queuedNext":0}
        """;

    private void Serve(string identity, string scope = "[]")
    {
        Answer("api/owned-scope/preview", NoImpact);
        Answer("api/owned-scope", scope);
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", identity);
    }

    private IEnumerable<SentRequest> Writes =>
        Sent.Where(r => r.Method == "PUT" && r.Url.Contains("api/owned-scope"));

    // Memberships call a security manager a member; the server's second read makes them eligible,
    // and the page says which role made it so rather than printing "member" beside a Declare button.
    [Fact]
    public void ASecurityManagerIsOfferedByTheRoleThatMakesThemEligible()
    {
        Serve(Identity);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declarable-toggle]")));
        cut.Find("[data-abom-declarable-toggle]").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declare='managed-org']")));
        Assert.Contains("security manager", cut.Find("[data-abom-declare='managed-org']").Closest("tr")!.TextContent);
        Assert.Empty(cut.FindAll("[data-abom-declare='member-org']"));
    }

    // A member may declare only the repositories they administer or maintain: the page offers that,
    // as an include list, and nothing they cannot claim can be chosen.
    [Fact]
    public void AMemberMayDeclareOnlyTheRepositoriesTheyMaintain()
    {
        Serve(Identity);
        AnswerWrite("api/accounts/member-org/repositories", MemberRepositories);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-members-toggle]")));
        cut.Find("[data-abom-members-toggle]").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declare-includes='member-org']")));
        cut.Find("[data-abom-declare-includes='member-org']").Click();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-repo-option='maintained'] input")));
        Assert.True(cut.Find("[data-abom-repo-option='written'] input").HasAttribute("disabled"));
        Assert.Contains("write", cut.Find("[data-abom-repo-option='written']").TextContent);
        cut.Find("[data-abom-repo-option='maintained'] input").Change(true);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Single(Writes));
        var body = Writes.Single().Body;
        Assert.Contains("\"member-org\"", body);
        Assert.Contains("\"includesOnly\":true", body);
        Assert.Contains("\"maintained\"", body);
        Assert.DoesNotContain("\"written\"", body);
    }

    // A role that could not be read is "cannot say", beside the membership, with what would let it answer.
    [Fact]
    public void ASecurityManagerRoleThatCannotBeReadSaysSoAndHow()
    {
        Serve(Identity);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-members-toggle]")));
        cut.Find("[data-abom-members-toggle]").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-security-manager='blind-org']")));
        var said = cut.Find("[data-abom-security-manager='blind-org']").TextContent;
        Assert.Contains("cannot say", said);
        Assert.Contains("admin:org", cut.Find("[data-abom-security-manager='blind-org']").OuterHtml);
        Assert.Empty(cut.FindAll("[data-abom-security-manager='member-org']"));
    }

    // Beside each declaration: whether the role behind it still supports it, each include's own
    // answer, an include the App's installation does not cover, and who installed the App.
    [Fact]
    public void EachDeclarationSaysWhetherItsRoleStillSupportsIt()
    {
        Serve(Identity, Checked);
        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-claim='member-org']")));
        Assert.Contains("not supported", cut.Find("[data-abom-claim='member-org']").TextContent);
        Assert.Contains("write", cut.Find("[data-abom-include-claim='written']").OuterHtml);
        Assert.Empty(cut.FindAll("[data-abom-include-claim='maintained']"));
        Assert.NotNull(cut.Find("[data-abom-include-not-installed='written']"));
        Assert.Empty(cut.FindAll("[data-abom-include-not-installed='maintained']"));
        Assert.Contains("octocat", cut.Find("[data-abom-installed-by='member-org']").TextContent);
        Assert.Equal(2, cut.FindAll("[data-abom-include]").Count);
    }

    // With an App, a check reads its installations; the page must not send a production reader to gh.
    [Fact]
    public void WithAnAppTheCheckNamesTheAppNotGh()
    {
        Serve(ByApp);
        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("App", cut.Find("[data-abom-check]").TextContent));
        Assert.DoesNotContain("gh", cut.Find("[data-abom-check]").TextContent);
        cut.Find("[data-abom-declarable-toggle]").Click();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declare='has-ghas']")));
        Assert.Contains("installed", cut.Find("[data-abom-declare='has-ghas']").Closest("tr")!.TextContent);
    }

    // A server reading as an App never runs gh, so any sentence naming gh — text, title or tooltip,
    // on a saved row, an unsaved one or one the App is not installed on — tells its reader something false.
    [Fact]
    public void WithAnAppNothingOnThePageNamesGh()
    {
        Serve(ByApp, AppScope);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declared='elsewhere']")));
        cut.Find("[data-abom-declarable-toggle]").Click();
        cut.Find("[data-abom-new-owner] input").Change("typed-org");
        cut.Find("[data-abom-declare-typed]").Click();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declared='typed-org']")));

        var said = cut.Nodes.Select(n => n.TextContent)
            .Concat(cut.FindAll("[title]").Select(e => e.GetAttribute("title") ?? ""))
            .Concat(cut.FindComponents<MudBlazor.MudTooltip>().Select(t => t.Instance.Text ?? ""));
        var gh = new System.Text.RegularExpressions.Regex(@"(?<![\w-])gh(?![\w-])");
        var naming = said.SelectMany(s => gh.Matches(s).Select(m =>
            s.Substring(Math.Max(0, m.Index - 50), Math.Min(s.Length, m.Index + 50) - Math.Max(0, m.Index - 50))));
        Assert.Empty(naming);
        Assert.Contains("Read as the App abom-has-ghas[bot]", cut.Find("[data-abom-identity]").TextContent);
        Assert.Contains("not installed", cut.Find("[data-abom-access='elsewhere']").TextContent);
    }

    // Configuring an App changes what the next check asks, not who the record on file was read as.
    [Fact]
    public void APersonsVerificationFromBeforeTheAppIsNotPresentedAsTheApps()
    {
        Serve(Identity.Replace("\"identityBy\":\"gh\"", "\"identityBy\":\"app\""));
        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("Verified as data-douser", cut.Find("[data-abom-identity]").TextContent));
        Assert.DoesNotContain("Read as the App", cut.Find("[data-abom-identity]").TextContent);
        Assert.Contains("App", cut.Find("[data-abom-check]").TextContent);
    }
}
