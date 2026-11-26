using Abom.WebAssembly.Models;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A refusal says what to do next where the interface holds the answer: a declaration it refused
/// is fixed in Settings, and a failure that may pass is read again, while one that will not pass by
/// asking again offers nothing to press.
/// </summary>
public sealed class AbomAlertTests : AbomTestContext
{
    private IRenderedComponent<Shared.AbomAlert> Alert(string code, int status) =>
        Render<Shared.AbomAlert>(p => p
            .Add(a => a.Problem, new ApiProblem { Code = code, Status = status, Detail = "why" })
            .Add(a => a.OnRetry, () => { }));

    [Theory]
    [InlineData("declaration-refused", 403)]
    [InlineData("declaration-refused-by-role", 403)]
    [InlineData("account-not-listable", 409)]
    [InlineData("scope-undecided", 409)]
    public void ADeclarationRefusedLeadsToTheDeclaration(string code, int status)
    {
        var next = Alert(code, status).Find("[data-abom-alert-next]");

        Assert.StartsWith("/settings", next.GetAttribute("href"));
    }

    [Theory]
    [InlineData(ApiProblem.UnreachableCode, 0, true)]
    [InlineData("unavailable", 503, true)]
    [InlineData("too-many-requests", 429, true)]
    [InlineData("bad-gateway", 502, true)]
    [InlineData("upstream-failed", 502, true)]
    [InlineData("review-stale", 409, true)]
    [InlineData("role-too-low", 403, false)]
    [InlineData("publisher-misinstalled", 409, false)]
    public void OnlyWhatMayPassIsOfferedAgain(string code, int status, bool offered)
    {
        Assert.Equal(offered, Alert(code, status).FindAll("[data-abom-alert-retry]").Count == 1);
    }
}
