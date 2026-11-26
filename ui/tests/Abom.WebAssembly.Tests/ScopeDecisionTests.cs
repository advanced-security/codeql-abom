using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A declaration drives discovery only under a signed decision that still matches it. The page says
/// when the one on record does not, and lets the operator decide it as it stands.
/// </summary>
public class ScopeDecisionTests : AbomTestContext
{
    private const string Identity = """
        {"authenticated":true,"login":"data-douser","host":"github.com",
         "verifiedAt":"2026-09-04T02:12:00Z","problem":null,"canReadMemberships":true,
         "accounts":[{"owner":"acme","kind":"organization","role":"admin","canDeclare":true}]}
        """;

    private const string DeclaresAcme = """
        [{"owner":"acme","kind":"organization","enabled":true,
          "includesOnly":false,"includes":[],"declaredAt":"2026-09-07T00:00:00Z",
          "declaredBy":"cli","state":"confirmed"}]
        """;

    // Registered before the declaration, which the decision's path also contains.
    private void Serve(string? decision)
    {
        if (decision is not null)
        {
            Answer("api/owned-scope/decision", decision);
        }
        Answer("api/owned-scope", DeclaresAcme);
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", Identity);
    }

    [Fact]
    public void ASignedDeclarationNeedsNothingSaid()
    {
        Serve("""{"state":"signed","reason":"","digest":"ab","decidedAt":"2026-11-25T00:00:00Z","decidedBy":"data-douser"}""");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() =>
        {
            Assert.Contains(Sent, r => r.Url.EndsWith("api/owned-scope/decision", StringComparison.Ordinal));
            Assert.NotEmpty(cut.FindAll("[data-abom-declared='acme']"));
        });
        Assert.Empty(cut.FindAll("[data-abom-scope-decision]"));
    }

    // A declaration with no decision on record names none, and discovery does not act on it.
    [Fact]
    public void AnUnsignedDeclarationSaysDiscoveryIgnoresItAndOffersToConfirmIt()
    {
        Serve("""{"state":"unsigned","reason":"no recorded decision shows that saving it was allowed","digest":"","decidedAt":"","decidedBy":""}""");
        AnswerWrite("api/owned-scope", DeclaresAcme);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-scope-decision='unsigned']")));
        var banner = cut.Find("[data-abom-scope-decision='unsigned']").TextContent;
        Assert.Contains("discovery does not act on it", banner, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("no recorded decision shows that saving it was allowed", banner, StringComparison.OrdinalIgnoreCase);

        cut.Find("[data-abom-confirm-scope]").Click();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Method == "PUT" && r.Url.EndsWith("api/owned-scope")));
        var put = Sent.Last(r => r.Method == "PUT");
        Assert.Contains("\"owner\":\"acme\"", put.Body);
    }

    // A refused confirmation says why where it was pressed, not beside Save at the foot of the page.
    [Fact]
    public void ARefusedConfirmationSaysWhyInTheBanner()
    {
        Serve("""{"state":"unsigned","reason":"no recorded decision shows that saving it was allowed","digest":"","decidedAt":"","decidedBy":""}""");
        RefuseWrite("api/owned-scope", System.Net.HttpStatusCode.Forbidden,
            "declaring acme that way is refused: octocat is a member of acme");

        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-confirm-scope]")));
        cut.Find("[data-abom-confirm-scope]").Click();

        cut.WaitForAssertion(() => Assert.Contains("octocat is a member of acme",
            cut.Find("[data-abom-scope-decision='unsigned']").TextContent));
    }

    // A signed declaration says who decided it and whose GitHub roles it was judged against: on a
    // hosted instance those are two different accounts, and the second is ABOM's own.
    [Fact]
    public void ASignedDeclarationNamesWhoDecidedItAndTheLoginItWasJudgedAs()
    {
        Serve("""{"state":"signed","reason":"","digest":"ab","decidedAt":"2026-11-25T10:00:00Z","decidedBy":"ada@example.com","judgedAs":"abom-reader[bot]"}""");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-scope-decided]")));
        var said = cut.Find("[data-abom-scope-decided]").TextContent;
        Assert.Contains("ada@example.com", said);
        Assert.Contains("abom-reader[bot]", said);
    }

    // Discovery standing still is the first thing the page says, not something found under the table.
    [Fact]
    public void AnUnsignedDeclarationIsSaidBeforeTheAccountsItDeclares()
    {
        Serve("""{"state":"unsigned","reason":"no recorded decision shows that saving it was allowed","digest":"","decidedAt":"","decidedBy":""}""");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-scope-decision='unsigned']")));
        var inOrder = cut.FindAll("[data-abom-scope-decision], #declared-accounts");
        Assert.Equal(2, inOrder.Count);
        Assert.True(inOrder[0].HasAttribute("data-abom-scope-decision"), "the banner comes after the accounts table");
    }

    // A declaration changed after it was decided is not confirmed by the page on its own say-so: you
    // review what it now says before deciding it. The link names the page, because against the
    // document's base a bare fragment is the Dashboard's.
    [Fact]
    public void ADeclarationThatNoLongerMatchesItsDecisionIsNamedAsChanged()
    {
        Serve("""{"state":"unverified","reason":"the declaration does not match its decision; it was changed after it was decided","digest":"ab","decidedAt":"","decidedBy":""}""");

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-scope-decision='unverified']")));
        var banner = cut.Find("[data-abom-scope-decision='unverified']").TextContent;
        Assert.Contains("changed after it was decided", banner);
        Assert.NotNull(cut.Find("[data-abom-scope-decision='unverified'] a[href='/settings#declared-accounts']"));
    }

    // Not being able to read the decision is not evidence against the declaration.
    [Fact]
    public void ADecisionThatCannotBeReadClaimsNothing()
    {
        Serve(null);

        var cut = Render<Pages.Settings>();

        cut.WaitForAssertion(() =>
        {
            Assert.Contains(Sent, r => r.Url.EndsWith("api/owned-scope/decision", StringComparison.Ordinal));
            Assert.NotEmpty(cut.FindAll("[data-abom-declared='acme']"));
        });
        Assert.Empty(cut.FindAll("[data-abom-scope-decision]"));
    }
}
