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
        Assert.Equal("http://localhost/", navigation.Uri);
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

    // A sign-in the server refused comes back to the interface, which says which kind it was.
    [Fact]
    public void ARefusedSignInIsExplainedOnArrival()
    {
        Answer("api/me", """{"mode":"signed-in","user":null,"may":[]}""");
        Services.GetRequiredService<NavigationManager>().NavigateTo("/?signin=refused");

        var cut = Render<Shared.AccessState>(p => p.AddChildContent("<p data-the-page>page</p>"));

        Assert.Contains("refused the sign-in", cut.WaitForElement("[data-abom-signin-refused='refused']").TextContent);
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
