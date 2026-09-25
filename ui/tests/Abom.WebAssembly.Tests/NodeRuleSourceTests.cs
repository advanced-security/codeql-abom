using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Where the query behind each rule came from is read from the evaluations that reported its
/// findings, so the page says so per rule, and says when it was not recorded.
/// </summary>
public class NodeRuleSourceTests : AbomTestContext
{
    private const string Detail = """
        {"key":"actions/checkout","kind":"action","repository":"","origin":"","path":"",
         "isExternal":true,"analyzed":true,"dependentRepos":2,"directDependents":3,"refs":[],
         "rules":[
           {"ruleId":"actions/unpinned-tag","count":3,"sources":["custom","upstream"],"unsourced":1},
           {"ruleId":"abom/secrets-inherit","count":2,"sources":["abom"],"unsourced":0},
           {"ruleId":"actions/untrusted-checkout","count":1,"sources":[],"unsourced":1}],
         "advisories":[],"usedBy":[],"uses":[]}
        """;

    private IRenderedComponent<Pages.NodePage> RenderNode()
    {
        Answer("/-/reach", "[]");
        Answer("/-/impact", """{"key":"actions/checkout","direct":0,"transitive":0,"repos":[],"unanalyzed":0}""");
        Answer("api/node/", Detail);
        var cut = Render<Pages.NodePage>(p => p.Add(c => c.Key, "actions/checkout"));
        cut.WaitForAssertion(() => cut.Find("[data-abom-rule='abom/secrets-inherit']"));
        return cut;
    }

    private static List<string> SourcesOf(IRenderedComponent<Pages.NodePage> cut, string rule) =>
        cut.Find($"[data-abom-rule='{rule}']").QuerySelectorAll("[data-abom-rule-source]")
            .Select(e => e.TextContent.Trim()).ToList();

    [Fact]
    public void EachRuleNamesTheSourcesThatReportedIt()
    {
        var cut = RenderNode();

        Assert.Equal(["custom", "upstream"], SourcesOf(cut, "actions/unpinned-tag"));
        Assert.Equal(["ABOM"], SourcesOf(cut, "abom/secrets-inherit"));
    }

    /// <summary>
    /// A finding no evaluation recorded a source for is not known to be from any, so the count of
    /// them is shown beside the sources that are known rather than folded into one of them.
    /// </summary>
    [Fact]
    public void FindingsWithNoRecordedSourceAreCountedRatherThanAttributed()
    {
        var cut = RenderNode();

        Assert.Equal("source not recorded for 1",
            cut.Find("[data-abom-rule='actions/unpinned-tag'] [data-abom-rule-unsourced]").TextContent.Trim());
        Assert.Empty(SourcesOf(cut, "actions/untrusted-checkout"));
        Assert.Equal("source not recorded",
            cut.Find("[data-abom-rule='actions/untrusted-checkout'] [data-abom-rule-unsourced]").TextContent.Trim());
        Assert.Empty(cut.FindAll("[data-abom-rule='abom/secrets-inherit'] [data-abom-rule-unsourced]"));
    }
}
