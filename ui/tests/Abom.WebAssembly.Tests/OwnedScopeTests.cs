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
         "verifiedAt":"2026-09-04T02:12:00Z","problem":null,"canReadMemberships":true,
         "accounts":[{"owner":"data-douser","kind":"user","role":"owner","canDeclare":true},
                     {"owner":"ops-team","kind":"organization","role":"admin","canDeclare":true},
                     {"owner":"docs","kind":"organization","role":"member","canDeclare":false}]}
        """;

    // The same identity with nothing on record about what the token may read. `user/memberships/orgs`
    // answers with what it can see, which is why absence from it proves nothing here.
    private const string BlindIdentity = """
        {"authenticated":true,"login":"data-douser","host":"github.com",
         "verifiedAt":"2026-09-04T02:12:00Z","problem":null,"canReadMemberships":false,
         "accounts":[{"owner":"data-douser","kind":"user","role":"owner","canDeclare":true}]}
        """;

    private const string Candidates = """
        [{"owner":"acme","repositories":4,"findings":9,"owned":false},
         {"owner":"docs","repositories":2,"findings":1,"owned":false}]
        """;

    private const string DeclaresAcme = """
        [{"owner":"acme","kind":"organization","enabled":true,
          "includesOnly":false,"includes":[],"declaredAt":"2026-09-07T00:00:00Z",
          "declaredBy":"interface","state":"unknown"}]
        """;

    private const string DeclaresRestricted = """
        [{"owner":"acme-labs","kind":"organization","enabled":true,
          "includesOnly":true,"includes":["tooling"],"declaredAt":"2026-09-07T00:00:00Z",
          "declaredBy":"interface","state":"unknown"}]
        """;

    // gh answered, listing 33 accounts, and this was not among them. The state that did not exist
    // until a corpus was found declaring `facebook` for someone who has never owned it.
    private const string DeclaresContradicted = """
        [{"owner":"facebook","kind":"organization","enabled":true,
          "includesOnly":false,"includes":[],"declaredAt":"2026-09-07T00:00:00Z",
          "declaredBy":"interface","state":"contradicted"}]
        """;

    private const string DeclaresUnaccounted = """
        [{"owner":"acme","kind":"organization","enabled":true,
          "includesOnly":false,"includes":[],"declaredAt":"2026-09-07T00:00:00Z",
          "declaredBy":"","state":"unknown"}]
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

    // A declaration gh contradicts is kept — you can be responsible for something you have just
    // lost access to — but it must not be drawn as settled fact. The green mark outweighed the
    // amber caption beside it, and `facebook` read as owned for as long as nobody looked.
    [Fact]
    public void AContradictedDeclarationDoesNotCarryTheOwnershipMark()
    {
        ServeEditable(DeclaresContradicted);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(
            () => Assert.NotNull(cut.Find("[data-abom-declared='facebook']")));
        Assert.DoesNotContain(
            "abom-owned-mark", cut.Find("[data-abom-declared='facebook']").GetAttribute("class"));
    }

    // Losing the network is not gh disagreeing. One is a missing measurement with a command that
    // fixes it; the other is evidence against.
    [Fact]
    public void AnUnverifiedDeclarationIsStillDrawnAsYours()
    {
        ServeEditable(DeclaresAcme);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declared='acme']")));
        Assert.Contains(
            "abom-owned-mark", cut.Find("[data-abom-declared='acme']").GetAttribute("class"));
    }

    [Fact]
    public void AContradictedDeclarationSaysGhDoesNotListIt()
    {
        ServeEditable(DeclaresContradicted);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(
            () => Assert.Contains("does not list",
                cut.Find("[data-abom-access='facebook']").TextContent));
    }

    // Since the gate, a contradicted declaration roots nothing: no crawl, no bill of materials,
    // no "yours" figure. An operator who is not told that reads every count on every page as
    // covering an account it silently excludes.
    [Fact]
    public void AContradictedDeclarationSaysNothingItHoldsIsCounted()
    {
        ServeEditable(DeclaresContradicted);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-held]")));
        Assert.Contains("facebook", cut.Find("[data-abom-held]").TextContent);
    }

    // Nothing is held while nothing is contradicted, or the warning becomes furniture.
    [Fact]
    public void AnUnverifiedDeclarationIsNotReportedAsHeld()
    {
        ServeEditable(DeclaresAcme);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declared='acme']")));
        Assert.Empty(cut.FindAll("[data-abom-held]"));
    }

    // Nothing on record saying the token can read organization memberships means gh can neither
    // confirm nor contradict, and the store cannot tell a token carrying no such scope from a
    // verification taken before scopes were recorded. Saying "press Check with gh" alone sends the
    // first operator round a loop that cannot terminate, so the caption has to cover both.
    [Fact]
    public void AnUnrecordedTokenScopeSaysSoRatherThanBlamingGh()
    {
        Answer("api/owned-scope/preview", NoImpact);
        Answer("api/owned-scope/enumerate",
            """{"added":0,"promoted":0,"unchanged":0,"accounts":[],"failed":[]}""");
        Answer("api/owned-scope", DeclaresAcme);
        Answer("api/owner-candidates", Candidates);
        Answer("api/accounts", BlindIdentity);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-uncheckable]")));
    }

    // The whole plan in one assertion: a row nobody can account for must say so, because that is
    // exactly what a declaration somebody typed into the database looks like.
    [Fact]
    public void ADeclarationNobodyCanAccountForSaysSo()
    {
        ServeEditable(DeclaresUnaccounted);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(
            () => Assert.NotNull(cut.Find("[data-abom-unaccounted='acme']")));
    }

    // A row the operator is adding right now is about to be accounted for by the save, and marking
    // it unknown-source would be telling them their own action is untraceable.
    [Fact]
    public void AnUnsavedDeclarationIsNotMarkedUnaccountedFor()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));
        OpenMemberships(cut);

        cut.Find("[data-abom-declare='ops-team']").Click();

        Assert.Empty(cut.FindAll("[data-abom-unaccounted='ops-team']"));
    }

    // The membership list is collapsed until asked for, so anything reaching into it opens it
    // first. Stated once here rather than as a click repeated in every test that declares.
    private static void OpenMemberships(IRenderedComponent<Pages.Settings> cut)
    {
        cut.WaitForAssertion(
            () => Assert.NotNull(cut.Find("[data-abom-memberships] .mud-expand-panel-header")));
        cut.Find("[data-abom-memberships] .mud-expand-panel-header").Click();
        cut.WaitForAssertion(
            () => Assert.NotEmpty(cut.FindAll("[data-abom-membership='ops-team']")));
    }

    private void ServeEditable(string scope)
    {
        // Registered before the scope stub: `api/owned-scope` is a prefix of both these paths, and
        // the stub that answers an overlapping path is the first one registered.
        Answer("api/owned-scope/preview", NoImpact);
        // Saving enumerates what was declared. These tests are about what gets written, not what
        // it turned out to hold, so the enumeration answers nothing found.
        Answer("api/owned-scope/enumerate",
            """{"added":0,"promoted":0,"unchanged":0,"accounts":[],"failed":[]}""");
        Serve(scope);
    }

    private IEnumerable<SentRequest> Writes =>
        Sent.Where(r => r.Method == "PUT" && r.Url.Contains("api/owned-scope"));

    [Fact]
    public void AnAccountYouBelongToCanBeDeclaredInOneClick()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));
        OpenMemberships(cut);

        cut.Find("[data-abom-declare='ops-team']").Click();
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Single(Writes));
        Assert.Contains("\"ops-team\"", Writes.Single().Body);
    }

    // Being in an organization is not being answerable for it. Offering every membership is how a
    // corpus came to assert this operator owns `facebook`, so a role that can change nothing is
    // listed without a button rather than offered.
    [Fact]
    public void AnAccountYouAreOnlyAMemberOfIsNotOfferedForDeclaring()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));
        OpenMemberships(cut);

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-membership='docs']")));
        Assert.Empty(cut.FindAll("[data-abom-declare='docs']"));
    }

    // Thirty-three accounts of which thirteen are declarable is a list that buries its own answer,
    // and this is a page people come to for one row at a time. Nothing is listed until it is asked
    // for — asserted on the membership grid, because `docs` also holds repositories in the corpus
    // and legitimately appears further down the page.
    [Fact]
    public void NoMembershipIsListedUntilTheSectionIsOpened()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));

        Assert.Empty(cut.FindAll("[data-abom-membership='ops-team']"));
        Assert.Empty(cut.FindAll("[data-abom-membership='docs']"));
    }

    // Opening it lists every account, including the ones that cannot be declared: the absence of a
    // button beside a name is the answer, and leaving the name out entirely was read as gh not
    // reporting the account at all.
    [Fact]
    public void OpeningTheSectionListsEveryAccountOnRecord()
    {
        ServeEditable("[]");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));
        OpenMemberships(cut);

        Assert.NotEmpty(cut.FindAll("[data-abom-membership='ops-team']"));
        Assert.NotEmpty(cut.FindAll("[data-abom-membership='docs']"));
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
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));

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
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));
        OpenMemberships(cut);

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
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));
        OpenMemberships(cut);

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
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-memberships]")));
        OpenMemberships(cut);

        cut.Find("[data-abom-declare='ops-team']").Click();

        cut.WaitForAssertion(() => Assert.Contains("reaches nothing", cut.Markup));
    }
}
