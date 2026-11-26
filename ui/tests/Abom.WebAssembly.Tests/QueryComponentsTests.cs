using Abom.WebAssembly.Shared;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Somebody who wants to publish a query needs every file that makes it run, not the four a step
/// opens. What is missing is named where it would be, a run's own output is marked as not part of
/// the bundle, and the newest test says which version and which CodeQL it describes.
/// </summary>
public class QueryComponentsTests : AbomTestContext
{
    private const string Bundle = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

    private static string Manifest(string newestTest, bool exportable = false) => $$"""
        {"query":"acme/a","version":"v2v2v2v2v2v2v2v2","pack":"abom/custom-queries","packVersion":"2.27.0",
         "dependencies":[{"name":"codeql/actions-all","version":"0.7.0"}],"outside":["actions"],
         "development":true,"deployed":false,"bundleSha256":"{{Bundle}}",
         "components":[
           {"role":"manifest","path":"qlpack.yml","sha256":"aa","size":120,"bundled":true},
           {"role":"query","path":"src/A.ql","sha256":"bb","size":300,"bundled":true},
           {"role":"library","path":"src/lib/Helper.qll","sha256":"cc","size":40,"bundled":true},
           {"role":"qlref","path":"test/A/A.qlref","sha256":"dd","size":20,"bundled":true},
           {"role":"actual","path":"test/A/A.actual","sha256":"ee","size":10,"bundled":false}],
         "missing":[{"role":"help","path":"src/A.md","why":"no help beside the query"}],
         "newestTest":{{newestTest}},"exportable":{{(exportable ? "true" : "false")}}}
        """;

    private IRenderedComponent<QueryComponents> Panel(string newestTest = "null", bool exportable = false)
    {
        Answer("api/queries/components?", Manifest(newestTest, exportable));
        Answer("api/queries/component?", """{"path":"src/A.ql","body":"/** @id acme/a */\nimport actions","truncated":false}""");
        var panel = Render<QueryComponents>(p => p.Add(c => c.QueryId, "acme/a"));
        panel.WaitForAssertion(() => Assert.NotEmpty(panel.FindAll("[data-abom-component]")));
        return panel;
    }

    private static void Choose(IRenderedComponent<QueryComponents> panel, string hook) =>
        panel.Find($"{hook} .mud-treeview-item-content").Click();

    [Fact]
    public void EveryComponentIsListedOnceUnderItsDirectoryAndWhatIsMissingIsNamedThere()
    {
        var panel = Panel();

        // Files before directories at each level, each by name, as the bundle lays them out.
        Assert.Equal(["qlpack.yml", "src/A.ql", "src/lib/Helper.qll", "test/A/A.actual", "test/A/A.qlref"],
            panel.FindAll("[data-abom-component]").Select(e => e.GetAttribute("data-abom-component")));
        Assert.Equal(["src", "src/lib", "test", "test/A"],
            panel.FindAll("[data-abom-component-directory]").Select(e => e.GetAttribute("data-abom-component-directory")));
        var missing = panel.Find("[data-abom-component-missing='src/A.md']");
        Assert.Equal("help", missing.GetAttribute("data-abom-component-role"));
        Assert.NotNull(panel.Find("[data-abom-component-directory='src'] [data-abom-component-missing='src/A.md']"));

        Choose(panel, "[data-abom-component-missing='src/A.md']");
        panel.WaitForAssertion(() =>
            Assert.Contains("no help beside the query", panel.Find("[data-abom-component-absent='src/A.md']").TextContent));
    }

    [Fact]
    public void WhatTheTestsLastWroteIsMarkedAsOutsideTheBundle()
    {
        var panel = Panel();

        Assert.Contains("not in the bundle", panel.Find("[data-abom-component='test/A/A.actual']").TextContent);
        Assert.DoesNotContain("not in the bundle", panel.Find("[data-abom-component='src/A.ql']").TextContent);
    }

    [Fact]
    public void AComponentOpensAsItIsOnDisk()
    {
        var panel = Panel();

        Choose(panel, "[data-abom-component='src/A.ql']");

        panel.WaitForAssertion(() =>
            Assert.Contains("import actions", panel.Find("[data-abom-component-view='src/A.ql']").TextContent));
        Assert.Contains(Sent, r => r.Url.Contains("api/queries/component?query=acme%2Fa&path=src%2FA.ql"));
    }

    [Fact]
    public void ComponentsThatCannotBeReadSayWhy()
    {
        Refuse("api/queries/components?", System.Net.HttpStatusCode.NotFound, "acme/a is not a query in the query directory");
        var panel = Render<QueryComponents>(p => p.Add(c => c.QueryId, "acme/a"));

        panel.WaitForAssertion(() =>
            Assert.Contains("not a query in the query directory", panel.Find("[data-abom-components-failed]").TextContent));
    }

    [Fact]
    public void TheBundleDownloadsUnderTheDigestItWasShownWith()
    {
        var panel = Panel();

        var link = panel.Find("[data-abom-component-bundle]");
        Assert.Equal("api/queries/bundle?query=acme%2Fa", link.GetAttribute("href"));
        Assert.NotNull(link.GetAttribute("download"));
        Assert.Equal(Bundle, panel.Find("[data-abom-component-digest]").GetAttribute("data-abom-component-digest"));
    }

    [Fact]
    public void ANeverTestedQuerySaysSoRatherThanShowingNoFailures()
    {
        var panel = Panel();

        Assert.Contains("never run", panel.Find("[data-abom-component-test]").TextContent);
    }

    [Fact]
    public void ATestOfAnotherVersionSaysItDoesNotDescribeTheOneOnDisk()
    {
        var panel = Panel("""{"queryId":"acme/a","queryVersion":"v1v1v1v1v1v1","cliVersion":"2.27.1","passed":true,"total":1,"failed":0,"ranAt":"2026-03-04T05:06:07Z"}""");

        var test = panel.Find("[data-abom-component-test]").TextContent;
        Assert.Contains("passed", test);
        Assert.Contains("CodeQL 2.27.1", test);
        Assert.Contains("an earlier version", test);
    }

    [Fact]
    public void WithNoPublisherTheExportIsOfferedDisabledSayingWhy()
    {
        var panel = Panel();

        Assert.Empty(panel.FindAll("[data-abom-component-export]"));
        var blocked = panel.Find("[data-abom-component-export-blocked]");
        Assert.True(blocked.HasAttribute("disabled"));
        Assert.Contains("app create --publisher", blocked.Closest("[data-abom-tip]")!.GetAttribute("data-abom-tip"));
    }

    [Fact]
    public void AnExportOpensAPullRequestAndBecomesItsLink()
    {
        AnswerWrite("api/queries/export", """
            {"url":"https://github.com/acme/queries/pull/7","target":"acme/queries","decision":"sha256:dd","bundle":"b"}
            """);
        var panel = Panel(exportable: true);

        panel.Find("[data-abom-component-export]").Click();

        panel.WaitForAssertion(() =>
        {
            var link = panel.Find("[data-abom-component-exported]");
            Assert.Equal("https://github.com/acme/queries/pull/7", link.GetAttribute("href"));
            Assert.Contains("acme/queries", link.TextContent);
        });
        // A second press would open the same pull request again; the link replaces the button.
        Assert.Empty(panel.FindAll("[data-abom-component-export]"));
        // The bundle sent is the one shown, so the server exports only what was reviewed.
        Assert.Contains(Sent, r => r.Method == "POST" && r.Url.Contains("api/queries/export?query=acme%2Fa&bundle=" + Bundle));
    }

    [Fact]
    public void ARefusedExportSaysWhyAndOpensNothing()
    {
        RefuseWrite("api/queries/export", System.Net.HttpStatusCode.Conflict,
            "the publisher App is installed on every repository of acme; choose only the repository exports go to");
        var panel = Panel(exportable: true);

        panel.Find("[data-abom-component-export]").Click();

        panel.WaitForAssertion(() =>
            Assert.Contains("every repository of acme", panel.Find("[data-abom-component-export-refused]").TextContent));
        Assert.Empty(panel.FindAll("[data-abom-component-exported]"));
    }
}
