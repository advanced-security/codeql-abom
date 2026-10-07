using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A reader who narrowed the details page to one version asked about that version: what its own
/// code holds and what it uses are read at the commit it resolves to, and a version nothing read is
/// not analyzed rather than clean.
/// </summary>
public class NodeAtTests : AbomTestContext
{
    private const string AtV1 = """
        {"key":"vendor/tool","kind":"composite_action","repository":"vendor/tool","origin":"upstream",
         "path":"","isExternal":false,"analyzed":true,"dependentRepos":1,
         "rules":[],"rulesUnread":["v1"],
         "uses":[{"key":"actions/cache","kind":"action","repository":"","origin":"","refs":["v3"],"count":1}],
         "usesUnread":[]}
        """;

    private const string Everywhere = """
        {"key":"vendor/tool","kind":"composite_action","repository":"vendor/tool","origin":"upstream",
         "path":"","isExternal":false,"analyzed":true,"dependentRepos":1,
         "rules":[{"ruleId":"acme/everywhere","count":1,"sources":[],"unsourced":0}],"rulesUnread":[],
         "uses":[],"usesUnread":[]}
        """;

    private IRenderedComponent<Pages.NodePage> RenderAt(string address)
    {
        Answer("/-/uses", """{"key":"vendor/tool","total":0,"items":[]}""");
        Answer("/-/versions", """{"key":"vendor/tool","total":0,"items":[],"advisories":[]}""");
        Answer("api/node/vendor/tool?at=v1", AtV1);
        Answer("api/node/vendor/tool", Everywhere);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo(address);
        return Render<Pages.NodePage>(p => p.Add(c => c.Key, "vendor/tool"));
    }

    [Fact]
    public void FindingsAndUsesAreReadAtTheVersionAskedFor()
    {
        var cut = RenderAt("/node/vendor/tool?at=v1");

        cut.WaitForAssertion(() => cut.Find("[data-abom-rules-unread]"));
        Assert.Contains(Sent, r => r.Url.Contains("api/node/vendor/tool?at=v1"));
        Assert.Contains("v1", cut.Find("[data-abom-rules-unread]").TextContent);
        Assert.DoesNotContain("No rule fired", cut.Markup);
        Assert.Empty(cut.FindAll("[data-abom-rule='acme/everywhere']"));
        Assert.Contains("v1", cut.Find("[data-abom-uses-at]").TextContent);
    }

    [Fact]
    public void WithoutAVersionTheyAreReadAcrossEveryVersionAnalyzed()
    {
        var cut = RenderAt("/node/vendor/tool");

        cut.WaitForAssertion(() => cut.Find("[data-abom-rule='acme/everywhere']"));
        Assert.Empty(cut.FindAll("[data-abom-rules-unread]"));
    }
}
