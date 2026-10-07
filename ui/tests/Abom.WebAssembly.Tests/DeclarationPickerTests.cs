using System.Net;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Declaring from what gh reports rather than from a name the reader already knows. The page asks
/// gh before it asks the reader, offers the declarable accounts first, and builds an include list
/// from GitHub's own answer — and never selects anything on the reader's behalf.
/// </summary>
public class DeclarationPickerTests : AbomTestContext
{
    private const string NeverVerified = """
        {"authenticated":false,"login":"","host":"","accounts":[],"problem":null,
         "canReadMemberships":false}
        """;

    private const string Identity = """
        {"authenticated":true,"login":"data-douser","host":"github.com",
         "verifiedAt":"2026-10-06T18:00:00Z","problem":null,"canReadMemberships":true,
         "accounts":[{"owner":"data-douser","kind":"user","role":"owner","canDeclare":true},
                     {"owner":"ops-team","kind":"organization","role":"admin","canDeclare":true},
                     {"owner":"docs","kind":"organization","role":"member","canDeclare":false}]}
        """;

    private const string NotAuthenticated = """
        {"authenticated":false,"login":"","host":"","accounts":[],"canReadMemberships":false,
         "problem":{"reason":"gh is not authenticated, so there is no way to confirm which accounts are yours.",
                    "remedy":"Run: gh auth login"}}
        """;

    private const string DeclaresRestricted = """
        [{"owner":"ops-team","kind":"organization","enabled":true,
          "includesOnly":true,"includes":["tooling","retired"],"declaredAt":"2026-10-06T00:00:00Z",
          "declaredBy":"interface","state":"confirmed"}]
        """;

    private const string OpsTeamRepositories = """
        {"owner":"ops-team","problem":null,"repositories":[
          {"name":"service","private":true,"archived":false,"fork":false},
          {"name":"tooling","private":false,"archived":false,"fork":false},
          {"name":"old-site","private":false,"archived":true,"fork":false}]}
        """;

    private const string NoImpact = """
        {"rootsNow":0,"rootsNext":0,"closureNow":0,"closureNext":0,
         "entering":[],"leaving":[],"enteringCount":0,"leavingCount":0,"truncated":false,
         "queuedNow":0,"queuedNext":0}
        """;

    private void Serve(string identity, string scope = "[]")
    {
        Answer("api/owned-scope/preview", NoImpact);
        Answer("api/owned-scope/enumerate",
            """{"added":0,"promoted":0,"unchanged":0,"accounts":[],"failed":[]}""");
        Answer("api/owned-scope", scope);
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", identity);
    }

    private IEnumerable<SentRequest> Checks =>
        Sent.Where(r => r.Method == "POST" && r.Url.Contains("api/accounts/refresh"));

    private IEnumerable<SentRequest> Listings =>
        Sent.Where(r => r.Method == "POST" && r.Url.Contains("/repositories"));

    private IEnumerable<SentRequest> Writes =>
        Sent.Where(r => r.Method == "PUT" && r.Url.Contains("api/owned-scope"));

    // A fresh install opened on a name field and a collapsed list that appeared only after a
    // separate button. The page now looks the accounts up itself before asking for anything.
    [Fact]
    public void TheFirstVisitAsksGhBeforeAskingTheReader()
    {
        Serve(NeverVerified);
        AnswerWrite("api/accounts/refresh", Identity);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("2 accounts",
            cut.Find("[data-abom-declarable-toggle]").TextContent));
        Assert.Single(Checks);
    }

    // Verification is the reader's own request, made once. An identity already on record is shown
    // as it stands; asking again is the button's job.
    [Fact]
    public void AnIdentityOnRecordIsNotCheckedAgainOnArrival()
    {
        Serve(Identity);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declarable-toggle]")));
        Assert.Empty(Checks);
    }

    // The answer arrives while the reader watches, so the page says what it is waiting for rather
    // than "no accounts verified", which is a claim about a question still in flight.
    [Fact]
    public void ACheckInFlightSaysSo()
    {
        Serve(NeverVerified);
        AnswerWrite("api/accounts/refresh", Identity);
        var release = Withhold("api/accounts/refresh");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-checking]")));
        Assert.DoesNotContain("No accounts verified yet", cut.Markup);
        release();
        cut.WaitForAssertion(() => Assert.Empty(cut.FindAll("[data-abom-checking]")));
    }

    [Fact]
    public void ACheckGhCouldNotAnswerNamesTheRemedy()
    {
        Serve(NeverVerified);
        AnswerWrite("api/accounts/refresh", NotAuthenticated);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.Contains("Run: gh auth login", cut.Markup));
    }

    // A server that cannot be reached is not gh saying no, and the page must not fall over with it.
    [Fact]
    public void ACheckTheServerRefusedIsReportedRatherThanThrown()
    {
        Serve(NeverVerified);
        Refuse("api/accounts/refresh", HttpStatusCode.InternalServerError, "the store is locked");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-check-failed]")));
    }

    // Asking once per visit to the application, not on every arrival at the page: a gh that is
    // broken stays broken until somebody fixes it, and re-running it on each visit only adds waits.
    [Fact]
    public void ACheckThatFailedIsNotRepeatedOnTheNextArrival()
    {
        Serve(NeverVerified);
        AnswerWrite("api/accounts/refresh", NotAuthenticated);

        var first = Render<Pages.Settings>();
        first.WaitForAssertion(() => Assert.Contains("Run: gh auth login", first.Markup));
        var second = Render<Pages.Settings>();
        second.WaitForAssertion(() => Assert.NotNull(second.Find("[data-abom-save]")));

        Assert.Single(Checks);
    }

    // Every declarable account at once buries the one row a reader came for, so the list is folded
    // behind its count until asked for, and drawn only once opened.
    [Fact]
    public void DeclarableAccountsAreFoldedUntilAskedFor()
    {
        Serve(Identity);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declarable-toggle]")));

        Assert.Empty(cut.FindAll("[data-abom-declare]"));
        Assert.Contains("2 accounts", cut.Find("[data-abom-declarable-toggle]").TextContent);
        OpenDeclarable(cut);

        Assert.NotEmpty(cut.FindAll("[data-abom-declare='data-douser']"));
        Assert.NotEmpty(cut.FindAll("[data-abom-declare='ops-team']"));
    }

    // The reader's own account is the one they are always answerable for, so it leads whatever
    // its name sorts as.
    [Fact]
    public void YourOwnAccountIsOfferedFirst()
    {
        Serve("""
            {"authenticated":true,"login":"zed","host":"github.com",
             "verifiedAt":"2026-10-06T18:00:00Z","problem":null,"canReadMemberships":true,
             "accounts":[{"owner":"ops-team","kind":"organization","role":"admin","canDeclare":true},
                         {"owner":"zed","kind":"user","role":"owner","canDeclare":true}]}
            """);

        var cut = Render<Pages.Settings>();
        OpenDeclarable(cut);

        var offered = cut.FindAll("[data-abom-declare]").Select(e => e.GetAttribute("data-abom-declare")).ToList();
        Assert.Equal(["zed", "ops-team"], offered);
    }

    // An account just chosen has no stored declaration for gh to have confirmed and no listing yet.
    // Drawing that in warning colors, beside a role gh has just reported, reads as a problem with
    // an account the reader picked from gh's own list.
    [Fact]
    public void AnUnsavedDeclarationIsNotWarnedAboutBeforeItExists()
    {
        Serve(Identity);
        var cut = Render<Pages.Settings>();
        OpenDeclarable(cut);

        cut.Find("[data-abom-declare='ops-team']").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-access='ops-team']")));
        Assert.Empty(cut.FindAll("[data-abom-access='ops-team'] .abom-unconfirmed"));
        Assert.Empty(cut.FindAll("[data-abom-listing='ops-team'] .abom-unconfirmed"));
        Assert.Contains("once saved", cut.Find("[data-abom-access='ops-team']").TextContent);
    }

    // Nothing is selected for the reader: an offered account is not a declared one until clicked
    // and saved.
    [Fact]
    public void OfferingAnAccountDeclaresNothing()
    {
        Serve(Identity);

        var cut = Render<Pages.Settings>();
        OpenDeclarable(cut);

        Assert.Empty(cut.FindAll("[data-abom-declared]"));
        Assert.Empty(Writes);
        Assert.True(cut.Find("[data-abom-save]").HasAttribute("disabled"));
    }

    // Twenty memberships that cannot be declared bury the twelve that can. They stay listed — an
    // absent name reads as gh not reporting the account — but folded until asked for, and never
    // with a button.
    [Fact]
    public void MembershipsThatCannotBeDeclaredAreFoldedAndCarryNoButton()
    {
        Serve(Identity);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-members-toggle]")));

        Assert.Empty(cut.FindAll("[data-abom-membership='docs']"));
        Assert.Contains("1 more", cut.Find("[data-abom-members-toggle]").TextContent);
        cut.Find("[data-abom-members-toggle]").Click();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-membership='docs']")));
        Assert.Contains("member", cut.Find("[data-abom-membership='docs']").Closest("tr")!.TextContent);
        Assert.Empty(cut.FindAll("[data-abom-declare='docs']"));
        Assert.Empty(cut.FindAll("[data-abom-declare]"));
    }

    // Both folds start closed and open on their own toggles; the declarable list is opened here.
    private static void OpenDeclarable(IRenderedComponent<Pages.Settings> cut)
    {
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declarable-toggle]")));
        cut.Find("[data-abom-declarable-toggle]").Click();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-declare]")));
    }

    // Declaring by name is for what gh cannot see, so it follows the list rather than leading it.
    [Fact]
    public void DeclaringByNameComesAfterWhatGhOffers()
    {
        Serve(Identity);
        var cut = Render<Pages.Settings>();
        OpenDeclarable(cut);

        var markup = cut.Markup;
        Assert.True(markup.IndexOf("data-abom-declare=\"ops-team\"", StringComparison.Ordinal)
                    < markup.IndexOf("data-abom-new-owner", StringComparison.Ordinal),
            "the name field is offered before the accounts gh reports");
    }

    // An account's repositories are read only when the reader asks to choose among them: listing
    // spends their token, and an organization of thousands is not read for a page view.
    [Fact]
    public void NoAccountIsListedUntilTheReaderAsksToChoose()
    {
        Serve(Identity, DeclaresRestricted);
        AnswerWrite("api/accounts/ops-team/repositories", OpsTeamRepositories);

        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-choose-repos='ops-team']")));

        Assert.Empty(Listings);
        cut.Find("[data-abom-choose-repos='ops-team']").Click();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-repo-option='service']")));
        Assert.Single(Listings);
    }

    [Fact]
    public void ARepositoryChosenFromTheListIsWhatGetsSaved()
    {
        Serve(Identity, DeclaresRestricted);
        AnswerWrite("api/accounts/ops-team/repositories", OpsTeamRepositories);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-choose-repos='ops-team']")));

        cut.Find("[data-abom-choose-repos='ops-team']").Click();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-repo-option='service'] input")));
        // What is already declared is shown chosen, so a reader is not invited to choose it twice.
        Assert.True(((AngleSharp.Html.Dom.IHtmlInputElement)cut.Find("[data-abom-repo-option='tooling'] input")).IsChecked);
        Assert.False(((AngleSharp.Html.Dom.IHtmlInputElement)cut.Find("[data-abom-repo-option='service'] input")).IsChecked);
        cut.Find("[data-abom-repo-option='service'] input").Change(true);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Single(Writes));
        var body = Writes.Single().Body;
        Assert.Contains("\"service\"", body);
        Assert.Contains("\"tooling\"", body);
    }

    // An include GitHub did not list — renamed, deleted, or never there — is still the reader's
    // statement and stays until they remove it, but it says GitHub did not name it.
    [Fact]
    public void AnIncludeGitHubDidNotListIsMarked()
    {
        Serve(Identity, DeclaresRestricted);
        AnswerWrite("api/accounts/ops-team/repositories", OpsTeamRepositories);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-choose-repos='ops-team']")));

        Assert.Empty(cut.FindAll("[data-abom-include-unlisted='retired']"));
        cut.Find("[data-abom-choose-repos='ops-team']").Click();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-include-unlisted='retired']")));
        Assert.Empty(cut.FindAll("[data-abom-include-unlisted='tooling']"));
    }

    // Typing filters what is offered; what is already chosen is shown whatever the filter says.
    [Fact]
    public void TheOfferedRepositoriesFollowTheFilter()
    {
        Serve(Identity, DeclaresRestricted);
        AnswerWrite("api/accounts/ops-team/repositories", OpsTeamRepositories);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-choose-repos='ops-team']")));
        cut.Find("[data-abom-choose-repos='ops-team']").Click();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-repo-option='old-site']")));

        cut.Find("[data-abom-repo-filter='ops-team'] input").Input("serv");

        cut.WaitForAssertion(() => Assert.Empty(cut.FindAll("[data-abom-repo-option='old-site']")));
        Assert.NotEmpty(cut.FindAll("[data-abom-repo-option='service']"));
        Assert.NotEmpty(cut.FindAll("[data-abom-include='tooling']"));
    }

    // An organization of thousands does not render thousands of rows: the first matches are drawn
    // and the rest are counted, so a reader knows to narrow rather than conclude it is missing.
    [Fact]
    public void ALongListSaysHowManyMoreMatch()
    {
        var many = string.Join(",", Enumerable.Range(0, 80).Select(i => $$"""{"name":"repo-{{i:000}}"}"""));
        Serve(Identity, DeclaresRestricted);
        AnswerWrite("api/accounts/ops-team/repositories",
            $$"""{"owner":"ops-team","problem":null,"repositories":[{{many}}]}""");
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-choose-repos='ops-team']")));

        cut.Find("[data-abom-choose-repos='ops-team']").Click();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-repo-option]")));
        Assert.Equal(50, cut.FindAll("[data-abom-repo-option]").Count);
        Assert.Contains("30 more match", cut.Find("[data-abom-repo-more='ops-team']").TextContent);
    }

    // When GitHub cannot be asked, the reader can still state the repositories: ownership is
    // declared, and a list that could not be read must not stop anyone declaring.
    [Fact]
    public void AListingGhRefusedLetsTheReaderNameRepositoriesInstead()
    {
        Serve(Identity, DeclaresRestricted);
        AnswerWrite("api/accounts/ops-team/repositories", """
            {"owner":"ops-team","repositories":[],
             "problem":{"reason":"ops-team requires single sign-on, and the gh token is not authorized for it.",
                        "remedy":"Open https://github.com/orgs/ops-team/sso to start a session."}}
            """);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-choose-repos='ops-team']")));

        cut.Find("[data-abom-choose-repos='ops-team']").Click();

        cut.WaitForAssertion(() => Assert.Contains("orgs/ops-team/sso", cut.Markup));
        Assert.False(cut.Find("[data-abom-includes-typed='ops-team'] input").HasAttribute("disabled"));
        cut.Find("[data-abom-includes-typed='ops-team'] input").Change("tooling, docs-site");
        cut.Find("[data-abom-save]").Click();
        cut.WaitForAssertion(() => Assert.Single(Writes));
        Assert.Contains("\"docs-site\"", Writes.Single().Body);
    }
}
