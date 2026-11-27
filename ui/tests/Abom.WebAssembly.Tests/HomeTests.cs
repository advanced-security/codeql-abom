using System.Net;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Home's figures are claims about what is yours. Each says what it does not know rather than
/// drawing a nought: a declaration nobody confirmed is not an empty one, and a read that failed
/// is not a count of nothing.
/// </summary>
public class HomeTests : AbomTestContext
{
    private const string Summary = """
        {"ownedRepos":0,"seedRepos":32,"ownedFindings":0,"findings":1395,"rules":11,
         "advisories":14,"affectedActions":12,"dependencies":74,"ownedExposures":0,"exposures":32}
        """;

    private const string Unsigned = """
        {"state":"unsigned","reason":"no recorded decision shows that saving it was allowed","digest":"","decidedAt":"","decidedBy":""}
        """;

    private static string Count(IRenderedComponent<Pages.Home> cut, string entry) =>
        cut.Find($"[data-abom-entry='{entry}'] [data-abom-count]").TextContent.Trim();

    // Measured on has-ghas: 32 repositories under an unconfirmed declaration, and Home said none.
    [Fact]
    public void AnUnconfirmedDeclarationIsSaidRatherThanCountedAsNone()
    {
        Answer("api/owned-scope/decision", Unsigned);
        Answer("api/summary", Summary);

        var cut = Render<Pages.Home>();

        cut.WaitForAssertion(() => Assert.Equal("not confirmed", Count(cut, "repositories")));
        Assert.Equal("not confirmed", Count(cut, "findings"));
        Assert.Equal("not confirmed", Count(cut, "exposure"));
        Assert.Equal("14", Count(cut, "advisories"));
        Assert.Contains("discovery does not act on it",
            cut.Find("[data-abom-declaration-state='unsigned']").TextContent, StringComparison.OrdinalIgnoreCase);
        Assert.NotNull(cut.Find("[data-abom-declaration-state='unsigned'] a[href='/settings?tab=accounts#declared-accounts']"));
    }

    // Nothing declared, and confirmed as nothing, is still none.
    [Fact]
    public void NothingDeclaredIsNone()
    {
        Answer("api/owned-scope/decision", """{"state":"none","reason":"","digest":"","decidedAt":"","decidedBy":""}""");
        Answer("api/summary", Summary);

        var cut = Render<Pages.Home>();

        cut.WaitForAssertion(() => Assert.Equal("none", Count(cut, "repositories")));
        Assert.Empty(cut.FindAll("[data-abom-declaration-state]"));
    }

    // A summary that could not be read says so; it never draws noughts as though it were counted.
    [Fact]
    public void ASummaryThatCouldNotBeReadIsNotDrawnAsNoughts()
    {
        Refuse("api/summary", HttpStatusCode.InternalServerError, "the store is unavailable");

        var cut = Render<Pages.Home>();

        cut.WaitForElement("[data-abom-summary-unread]");
        Assert.All(cut.FindAll("[data-abom-count]"), c => Assert.Equal("not read", c.TextContent.Trim()));
    }
}
