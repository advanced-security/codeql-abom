using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Ownership stopped being read from <c>gh</c> on 2026-09-07. These pin what the page must let an
/// operator state, and what it must not claim on their behalf.
/// </summary>
public class OwnedScopeTests : AbomTestContext
{
    private const string Identity = """
        {"authenticated":true,"login":"data-douser","host":"github.com",
         "verifiedAt":"2026-09-04T02:12:00Z","problem":null,
         "accounts":[{"owner":"data-douser","kind":"user","role":"owner","canDeclare":true},
                     {"owner":"ops-team","kind":"organization","role":"admin","canDeclare":true},
                     {"owner":"docs","kind":"organization","role":"member","canDeclare":false}]}
        """;

    private const string Candidates = """
        [{"owner":"acme","repositories":4,"findings":9,"owned":false},
         {"owner":"docs","repositories":2,"findings":1,"owned":false}]
        """;

    private const string DeclaresAcme = """
        [{"owner":"acme","kind":"organization","enabled":true,
          "includesOnly":false,"includes":[],"declaredAt":"2026-09-07T00:00:00Z"}]
        """;

    private const string DeclaresRestricted = """
        [{"owner":"acme-labs","kind":"organization","enabled":true,
          "includesOnly":true,"includes":["tooling"],"declaredAt":"2026-09-07T00:00:00Z"}]
        """;

    private void Serve(string scope)
    {
        Answer("api/owned-scope", scope);
        Answer("api/owner-candidates", Candidates);
        Answer("api/accounts", Identity);
    }

    // The whole complaint: `gh` reports 33 accounts because it reports membership, and being in
    // the `docs` organization does not make its supply chain anyone's remediation work.
    [Fact]
    public void AnAccountYouMerelyBelongToIsNotMarkedYours()
    {
        Serve("[]");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("acme", cut.Markup));
        Assert.DoesNotContain("abom-owned-mark", cut.Markup);
    }

    [Fact]
    public void ADeclaredAccountIsMarkedYours()
    {
        Serve(DeclaresAcme);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("abom-owned-mark", cut.Markup));
    }

    // Discovery refuses to start without one, so a page that does not say the scope is empty
    // leaves the operator looking at a disabled control with no explanation.
    [Fact]
    public void AnEmptyScopeSaysDiscoveryWillNotRun()
    {
        Serve("[]");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("will not start", cut.Markup));
    }

    [Fact]
    public void ADeclaredScopeDoesNotWarnThatDiscoveryIsBlocked()
    {
        Serve(DeclaresAcme);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("acme", cut.Markup));
        Assert.DoesNotContain("will not start", cut.Markup);
    }

    // An account restricted to two repositories reads identically to one covering all of them
    // unless the restriction is drawn, and the difference is the whole point of an include list.
    [Fact]
    public void AnAccountRestrictedToSomeRepositoriesSaysWhich()
    {
        Serve(DeclaresRestricted);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("tooling", cut.Markup));
    }

    // A declaration `gh` cannot confirm is kept and flagged: you can be responsible for something
    // you have just lost access to, and refusing to hold that is less useful than saying it.
    [Fact]
    public void ADeclaredAccountVerificationDoesNotConfirmIsFlaggedNotDropped()
    {
        Serve(DeclaresAcme);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("acme", cut.Markup));
        Assert.Contains("not confirmed", cut.Markup);
    }

    // A request in flight is not an empty declaration. Telling an operator discovery will not run
    // while the answer is still arriving is the regression this repository has shipped once.
    [Fact]
    public void TheScopeIsNotReportedEmptyWhileItIsStillLoading()
    {
        Answer("api/owner-candidates", Candidates);
        Answer("api/accounts", Identity);
        Withhold("api/owned-scope");

        var cut = Render<Pages.Settings>();

        Assert.DoesNotContain("will not start", cut.Markup);
    }

    // Everything below pins the editor. Before it existed the page rendered the declaration and
    // offered no way to make one, so a first-run operator read "something you state" and had
    // nothing to state it with.

    private const string NoImpact = """
        {"rootsNow":0,"rootsNext":0,"closureNow":0,"closureNext":0,
         "entering":[],"leaving":[],"enteringCount":0,"leavingCount":0,"truncated":false,
         "queuedNow":0,"queuedNext":0}
        """;

    private void ServeEditable(string scope)
    {
        // Registered before the scope stub: `api/owned-scope` is a prefix of this path, and the
        // stub that answers an overlapping path is the first one registered.
        Answer("api/owned-scope/preview", NoImpact);
        Serve(scope);
    }

    private IEnumerable<SentRequest> Writes =>
        Sent.Where(r => r.Method == "PUT" && r.Url.Contains("api/owned-scope"));

    [Fact]
    public void AnAccountYouBelongToCanBeDeclaredInOneClick()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("ops-team", cut.Markup));

        cut.Find("[data-abom-declare='ops-team']").Click();
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Single(Writes));
        Assert.Contains("\"ops-team\"", Writes.Single().Body);
    }

    // Being in an organization is not being answerable for it. Offering every membership is how a
    // corpus came to assert this operator owns `facebook`, so a role that can change nothing is
    // not offered at all.
    [Fact]
    public void AnAccountYouAreOnlyAMemberOfIsNotOfferedForDeclaring()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("ops-team", cut.Markup));

        cut.Find("[data-abom-show-all] input").Change(true);

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-membership='docs']")));
        Assert.Empty(cut.FindAll("[data-abom-declare='docs']"));
    }

    // Thirty-three accounts of which thirteen are declarable is a list that buries its own answer.
    // Asserted on the membership grid rather than the markup: `docs` also holds repositories in
    // the corpus, so it legitimately appears further down the page.
    [Fact]
    public void AnAccountYouCannotDeclareIsOutOfTheWayUntilAskedFor()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-membership='ops-team']")));

        Assert.Empty(cut.FindAll("[data-abom-membership='docs']"));
    }

    // The role is half of what "confirmed by gh" is confirming, and a declaration carrying a role
    // that can change nothing is exactly the state this page exists to make visible.
    [Fact]
    public void ADeclaredAccountShowsTheRoleGhReportsForIt()
    {
        ServeEditable("""
            [{"owner":"ops-team","kind":"organization","enabled":true,
              "includesOnly":false,"includes":[],"declaredAt":"2026-09-07T00:00:00Z"}]
            """);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(
            () => Assert.Equal("admin", cut.Find("[data-abom-role='ops-team']").TextContent.Trim()));
    }

    // A declaration gh does not report is kept, and must say that rather than showing an empty
    // cell that reads as a role of none.
    [Fact]
    public void ADeclaredAccountGhDoesNotReportSaysSoRatherThanShowingNoRole()
    {
        ServeEditable(DeclaresAcme);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(
            () => Assert.Contains("not reported", cut.Find("[data-abom-role='acme']").TextContent));
    }

    // Ownership is stated, not inferred, so the editor must not restrict the statement to what a
    // token happens to see. An operator can be responsible for an account gh cannot report.
    [Fact]
    public void AnAccountGhDoesNotReportCanStillBeDeclared()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("ops-team", cut.Markup));

        cut.Find("[data-abom-new-owner] input").Change("private-org");
        cut.Find("[data-abom-declare-typed]").Click();
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Single(Writes));
        Assert.Contains("private-org", Writes.Single().Body);
    }

    // The store holds `includesOnly` rather than deriving it from the list being non-empty,
    // precisely so clearing the last include cannot widen an account from two repositories to all
    // of them. An editor that sent the derived answer would reintroduce that on the way out.
    [Fact]
    public void ClearingTheLastIncludeDoesNotWidenTheAccount()
    {
        ServeEditable(DeclaresRestricted);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("tooling", cut.Markup));

        cut.Find("[data-abom-includes='acme-labs'] input").Change(string.Empty);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Single(Writes));
        var body = Writes.Single().Body;
        Assert.Contains("\"includesOnly\":true", body.Replace(" ", string.Empty));
    }

    // Undeclaring is how a corpus silently shrinks. What it costs is stated before the write,
    // because afterwards the evidence is exactly what has gone missing.
    [Fact]
    public void UndeclaringSaysWhatLeavesBeforeItIsWritten()
    {
        Answer("api/owned-scope/preview", """
            {"rootsNow":3,"rootsNext":0,"closureNow":9,"closureNext":0,
             "entering":[],"leaving":["acme/service","acme/tooling"],
             "enteringCount":0,"leavingCount":2,"truncated":false,
             "queuedNow":41,"queuedNext":0}
            """);
        Serve(DeclaresAcme);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("abom-owned-mark", cut.Markup));

        cut.Find("[data-abom-undeclare='acme']").Click();

        cut.WaitForAssertion(() => Assert.Contains("acme/service", cut.Markup));
        Assert.Empty(Writes);
    }

    // Editing a draft is not declaring. A page that wrote on every keystroke would make canceling
    // impossible and every mistake a commitment.
    [Fact]
    public void NothingIsWrittenUntilTheSaveIsPressed()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("ops-team", cut.Markup));

        cut.Find("[data-abom-declare='ops-team']").Click();

        Assert.Empty(Writes);
    }

    // A save changes everything downstream and used to say nothing about it, leaving the operator
    // on a page that looked the same as before they acted.
    [Fact]
    public void AfterASaveThePageNamesTheNextAction()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("ops-team", cut.Markup));

        cut.Find("[data-abom-declare='ops-team']").Click();
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-next-action]")));
    }

    // Declaring an account the corpus holds nothing from is legitimate — it is a statement of
    // intent — but silently succeeding leaves every downstream page empty with no explanation.
    [Fact]
    public void DeclaringSomethingThatReachesNothingSaysSo()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("ops-team", cut.Markup));

        cut.Find("[data-abom-declare='ops-team']").Click();

        cut.WaitForAssertion(() => Assert.Contains("reaches nothing", cut.Markup));
    }
}
