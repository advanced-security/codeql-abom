using System.Net;
using System.Text;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.AspNetCore.Components;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// With sign-in on, the page says who is signed in and under which role, and sends anyone the API
/// will not answer to sign in rather than showing empty pages that read as an empty estate.
/// </summary>
public class SignInTests : AbomTestContext
{
    private const string Operator = """
        {"mode":"signed-in","user":{"id":"https://idp.example#s1","name":"Ada Lovelace","username":"ada@example.com","role":"operator"},
         "may":["abom/discovery-control"]}
        """;

    // The account menu renders into the popover root, as the layout's does.
    private (IRenderedComponent<MudBlazor.MudPopoverProvider> Menu, IRenderedComponent<Shared.SignedIn> Bar) Account()
    {
        var menu = Render<MudBlazor.MudPopoverProvider>();
        var bar = Render<Shared.SignedIn>();
        bar.WaitForElement("[data-abom-account-avatar]").Click();
        menu.WaitForElement("[data-abom-account]");
        return (menu, bar);
    }

    [Fact]
    public void ASignedInPersonSeesWhoTheyAreTheirRoleAndWhatItLetsThemDo()
    {
        Answer("api/me", Operator);

        var (menu, _) = Account();

        var shown = menu.Find("[data-abom-account]").TextContent;
        Assert.Contains("Ada Lovelace", shown);
        Assert.Contains("ada@example.com", shown);
        Assert.Contains("operator", menu.Find("[data-abom-account-role]").TextContent);
        Assert.Contains("Start, pause and steer discovery", menu.Find("[data-abom-account-may]").TextContent);
        Assert.NotEmpty(menu.FindAll("[data-abom-sign-out]"));
    }

    // An administrator may do everything, which a list of every operation says less clearly.
    [Fact]
    public void TheMenuTellsAnAdministratorTheirRoleLetsThemDoEverything()
    {
        Answer("api/me", GitHubAdmin);

        var (menu, _) = Account();

        Assert.Contains("Every operation ABOM has", menu.Find("[data-abom-account-may]").TextContent);
        Assert.Empty(menu.FindAll("[data-abom-account-may] li"));
    }

    [Fact]
    public void APersonWithNoRoleIsToldSo()
    {
        Answer("api/me", Operator.Replace("\"role\":\"operator\"", "\"role\":\"\""));

        var (menu, _) = Account();

        Assert.Contains("No ABOM role", menu.Find("[data-abom-account-role]").TextContent);
    }

    // At this machine without sign-in there is nobody signed in, so nothing is shown.
    [Fact]
    public void WithoutSignInNoAccountIsShown()
    {
        Answer("api/me", """{"mode":"local","user":null,"may":[]}""");

        var cut = Render<Shared.SignedIn>();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.EndsWith("api/me")));
        Assert.Empty(cut.FindAll("[data-abom-signed-in]"));
    }

    [Fact]
    public void SigningOutEndsTheSessionAndLeaves()
    {
        Answer("api/me", Operator);
        AnswerWrite("auth/logout", "{}");
        var navigation = Services.GetRequiredService<NavigationManager>();
        navigation.NavigateTo("inventory");
        var (menu, _) = Account();

        menu.Find("[data-abom-sign-out]").Click();

        menu.WaitForAssertion(() => Assert.Contains(Sent, r => r.Method == "POST" && r.Url.EndsWith("auth/logout")));
        Assert.Equal("http://localhost/auth/?signin=signed-out", navigation.Uri);
    }

    // A session that did not end is not reported as one that did.
    [Fact]
    public void ASignOutThatFailedSaysSoAndStays()
    {
        Answer("api/me", Operator);
        RefuseWrite("auth/logout", HttpStatusCode.InternalServerError, "the session store is unavailable");
        var navigation = Services.GetRequiredService<NavigationManager>();
        navigation.NavigateTo("inventory");
        var (menu, bar) = Account();

        menu.Find("[data-abom-sign-out]").Click();

        bar.WaitForElement("[data-abom-account-avatar]").Click();
        menu.WaitForElement("[data-abom-sign-out-failed]");
        Assert.EndsWith("/inventory", navigation.Uri);
    }

    // Signed in with no role, every page would fail on its own; the page says why once instead.
    [Fact]
    public void AnAccountWithNoRoleSeesOneStateInsteadOfThePage()
    {
        Answer("api/me", Operator.Replace("\"role\":\"operator\"", "\"role\":\"\""));

        var cut = Render<Shared.AccessState>(p => p.AddChildContent("<p data-the-page>page</p>"));

        cut.WaitForElement("[data-abom-no-role]");
        Assert.Empty(cut.FindAll("[data-the-page]"));
    }

    // Until ABOM has said who is asking, no page is drawn: one drawn first fetches figures the server
    // may refuse, and shows them as none.
    [Fact]
    public void NoPageIsDrawnUntilABOMHasSaidWhoIsAsking()
    {
        var release = Withhold("api/me");
        Answer("api/me", Operator);

        var cut = Render<Shared.AccessState>(p => p.AddChildContent("<p data-the-page>page</p>"));

        Assert.Empty(cut.FindAll("[data-the-page]"));
        release();
        cut.WaitForElement("[data-the-page]");
    }

    // Who is asking could not be read: the page is drawn, and says for itself what it could not read.
    [Fact]
    public void APageIsDrawnWhenWhoIsAskingCannotBeRead()
    {
        Refuse("api/me", HttpStatusCode.InternalServerError, "the store is unavailable");

        var cut = Render<Shared.AccessState>(p => p.AddChildContent("<p data-the-page>page</p>"));

        cut.WaitForElement("[data-the-page]");
    }

    private const string GitHubAdmin = """
        {"mode":"signed-in","user":{"id":"https://github.com#583231","name":"The Octocat","username":"octocat","role":"admin",
          "claimed":["acme","acme:owners"],"grants":[{"by":"acme","role":"viewer"},{"by":"acme:owners","role":"admin"}],
          "provider":"GitHub","expires":"2026-11-25T20:00:00Z"},
         "may":["abom/declare-scope","abom/configure"],"needs":{"abom/declare-scope":"operator","abom/configure":"admin"}}
        """;

    // The bar says who is signed in, by the name the provider knows them by, and under which role,
    // without opening anything: initials alone did not tell a person the account was theirs.
    [Fact]
    public void TheBarSaysWhoIsSignedInAndUnderWhichRole()
    {
        Answer("api/me", GitHubAdmin);

        var bar = Render<Shared.SignedIn>();

        Assert.Equal("octocat", bar.WaitForElement("[data-abom-account-login]").TextContent.Trim());
        Assert.Equal("admin", bar.Find("[data-abom-account-bar-role]").TextContent.Trim());
    }

    // Settings says who you are to ABOM: the account and who vouched for it, the role and what
    // granted it, what the role lets you do, when the session ends, and that ABOM reads GitHub as
    // its App rather than as you.
    [Fact]
    public void SettingsSaysWhoYouAreToABOM()
    {
        Answer("api/me", GitHubAdmin);
        var app = new Models.AccountIdentity { Authenticated = true, Login = "abom-acme[bot]", Host = "github.com", IdentityBy = "app", ReadByApp = true };

        var cut = Render<Shared.WhoYouAre>(p => p.Add(x => x.Reader, app));

        var you = cut.WaitForElement("[data-abom-you='signed-in']").TextContent;
        Assert.Contains("The Octocat", you);
        Assert.Contains("octocat", you);
        Assert.Contains("GitHub", cut.Find("[data-abom-you-provider]").TextContent);
        Assert.Contains("admin", cut.Find("[data-abom-you-role]").TextContent);
        var granted = cut.FindAll("[data-abom-you-grant]").Select(g => g.TextContent.Trim()).ToList();
        Assert.Equal(["owner of acme: admin", "member of acme: viewer"], granted);
        Assert.Contains("the highest applies", cut.Find("[data-abom-you-granted]").TextContent);
        Assert.Contains("every operation", cut.Find("[data-abom-you-may]").TextContent);
        Assert.NotEmpty(cut.FindAll("[data-abom-you-expires]"));
        Assert.Contains("its own GitHub App, abom-acme[bot]", you);
    }

    // Below admin, what the role allows is listed, and what it does not is named with the role it needs.
    [Fact]
    public void ARoleBelowAdminSaysWhatNeedsAHigherOne()
    {
        Answer("api/me", """
            {"mode":"signed-in","user":{"id":"https://github.com#1","name":"","username":"vera","role":"viewer",
              "claimed":["acme"],"grants":[{"by":"acme","role":"viewer"}],"provider":"GitHub"},
             "may":["abom/saved-view"],"needs":{"abom/saved-view":"viewer","abom/declare-scope":"operator","abom/configure":"admin"}}
            """);

        var cut = Render<Shared.WhoYouAre>();

        Assert.Contains("Save named views", cut.WaitForElement("[data-abom-you-may]").TextContent);
        var higher = cut.Find("[data-abom-you-needs]").TextContent;
        Assert.Contains("Declare the accounts ABOM covers (operator)", higher);
        Assert.Contains("Change how ABOM is configured (admin)", higher);
        Assert.DoesNotContain("Save named views", higher);
    }

    // An identity provider's app roles and groups are its own words, and are shown as they came.
    [Fact]
    public void WhatAnIdentityProviderGrantedIsShownInItsWords()
    {
        Answer("api/me", Operator.Replace("\"role\":\"operator\"", "\"role\":\"operator\",\"claimed\":[\"ABOM.Operator\",\"Other\"],\"grants\":[{\"by\":\"ABOM.Operator\",\"role\":\"operator\"}],\"provider\":\"login.example.com\""));

        var cut = Render<Shared.WhoYouAre>();

        Assert.Equal("ABOM.Operator: operator", cut.WaitForElement("[data-abom-you-grant]").TextContent.Trim());
        Assert.Contains("login.example.com", cut.Find("[data-abom-you-provider]").TextContent);
    }

    // With sign-in off ABOM listens on this machine alone and cannot tell who you are, so Settings
    // says both, and that every change is recorded as this machine's.
    [Fact]
    public void WithoutSignInSettingsSaysOnlyThisMachineReachesIt()
    {
        Answer("api/me", """{"mode":"local","user":null,"may":[]}""");

        var cut = Render<Shared.WhoYouAre>();

        var local = cut.WaitForElement("[data-abom-you='local']").TextContent;
        Assert.Contains("Sign-in is off", local);
        Assert.Contains("only on this machine's loopback address", local);
        Assert.Contains("recorded as made at this machine", local);
        Assert.DoesNotContain("Anybody", local);
    }

    // Unread is not nobody: Settings says it could not tell, rather than that sign-in is off.
    [Fact]
    public void WhoYouAreUnreadIsSaidToBeUnknown()
    {
        Refuse("api/me", HttpStatusCode.InternalServerError, "the store is unavailable");

        var cut = Render<Shared.WhoYouAre>();

        cut.WaitForElement("[data-abom-you='unknown']");
        Assert.Empty(cut.FindAll("[data-abom-you='local']"));
    }

    // An API answer that says to sign in takes the browser there, remembering where it was.
    [Fact]
    public async Task AnAnswerThatSaysToSignInGoesThere()
    {
        var navigation = Services.GetRequiredService<NavigationManager>();
        navigation.NavigateTo("inventory?tab=coverage");
        using var client = new HttpClient(new SignInRequestHandler(navigation)
        {
            InnerHandler = new Respond(HttpStatusCode.Unauthorized, """{"type":"urn:abom:problem:sign-in-required","status":401,"code":"sign-in-required","detail":"sign in to use ABOM","login":"/auth/login"}"""),
        }) { BaseAddress = new Uri("http://localhost/") };

        using var response = await client.GetAsync("api/summary", Xunit.TestContext.Current.CancellationToken);

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
        Assert.EndsWith("auth/login?return=%2Finventory%3Ftab%3Dcoverage", navigation.Uri);
    }

    // A refusal that is not about signing in is the page's to explain, not a reason to leave it.
    [Fact]
    public async Task AnyOtherRefusalStaysOnThePage()
    {
        var navigation = Services.GetRequiredService<NavigationManager>();
        navigation.NavigateTo("inventory");
        using var client = new HttpClient(new SignInRequestHandler(navigation)
        {
            InnerHandler = new Respond(HttpStatusCode.Unauthorized, """{"type":"urn:abom:problem:unauthorized","status":401,"code":"unauthorized","detail":"the token was refused"}"""),
        }) { BaseAddress = new Uri("http://localhost/") };

        using var response = await client.GetAsync("api/summary", Xunit.TestContext.Current.CancellationToken);

        Assert.EndsWith("/inventory", navigation.Uri);
    }

    private sealed class Respond(HttpStatusCode status, string body) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
            Task.FromResult(new HttpResponseMessage(status) { Content = new StringContent(body, Encoding.UTF8, "application/json") });
    }
}
