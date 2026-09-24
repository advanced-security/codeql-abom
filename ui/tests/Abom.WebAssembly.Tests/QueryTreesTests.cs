using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A query's trees are read on its catalog row, each saying whether an agent's session or ABOM
/// printed it, because an agent steered one and nothing steered the other.
/// </summary>
public class QueryTreesTests : AbomTestContext
{
    private static readonly Models.CatalogQuery Query = new()
    {
        QueryId = "acme/self-hosted-runner", QueryVersion = "v1", Origin = "user", Working = true,
    };

    private const string Trees = """
        {"asts":[
          {"rowId":9,"sessionId":0,"requestId":0,"printedBy":"operator","queryId":"acme/self-hosted-runner",
           "printedAt":"2026-09-24T09:00:00Z","query":"PrintAST","database":"test/SelfHostedRunner/SelfHostedRunner.testproj",
           "sourceFiles":".github/workflows/runners.yml","testDigest":"d","size":2048,"truncated":false},
          {"rowId":4,"sessionId":2,"requestId":3,"printedBy":"","queryId":"acme/self-hosted-runner",
           "printedAt":"2026-09-23T09:00:00Z","query":"PrintAST","database":"test/SelfHostedRunner/SelfHostedRunner.testproj",
           "sourceFiles":".github/workflows/runners.yml","testDigest":"c","size":1024,"truncated":false}]}
        """;

    [Fact]
    public void EachTreeSaysWhoPrintedItAndIsShownWhenAskedFor()
    {
        Answer("api/queries/asts?query=acme%2Fself-hosted-runner", Trees);
        Answer("api/authoring-asts/9", """
            {"rowId":9,"sessionId":0,"requestId":0,"printedBy":"operator","queryId":"acme/self-hosted-runner",
             "printedAt":"2026-09-24T09:00:00Z","query":"PrintAST","database":"d","sourceFiles":"s",
             "testDigest":"d","size":2048,"truncated":false,"body":"#    1| [WorkflowImpl] name: runners\n"}
            """);
        var cut = Render<Shared.QueryTrees>(p => p.Add(c => c.Query, Query));

        cut.WaitForAssertion(() => Assert.Equal(2, cut.FindAll("[data-abom-ast]").Count));
        Assert.Contains("printed by ABOM for operator", cut.Find("[data-abom-ast='9']").TextContent);
        Assert.Contains("session #2", cut.Find("[data-abom-ast='4']").TextContent);

        cut.Find("[data-abom-ast='9'] [data-abom-ast-show]").Click();

        cut.WaitForAssertion(() => Assert.Contains("[WorkflowImpl] name: runners",
            cut.Find("[data-abom-ast-body]").TextContent));
    }

    [Fact]
    public void AQueryNothingPrintedSaysSo()
    {
        Answer("api/queries/asts?query=acme%2Fself-hosted-runner", """{"asts":[]}""");
        var cut = Render<Shared.QueryTrees>(p => p.Add(c => c.Query, Query));

        cut.WaitForAssertion(() => Assert.Contains("No tree", cut.Find("[data-abom-query-trees]").TextContent));
    }

    /// <summary>
    /// The extractor shapes what a tree says, so each names the CodeQL that printed it, and one kept
    /// before that was recorded says so rather than naming none.
    /// </summary>
    [Fact]
    public void EachTreeNamesTheCodeQLThatPrintedIt()
    {
        Answer("api/queries/asts?query=acme%2Fself-hosted-runner",
            Trees.Replace("\"size\":2048,", "\"size\":2048,\"cliVersion\":\"2.27.1\","));
        var cut = Render<Shared.QueryTrees>(p => p.Add(c => c.Query, Query));

        cut.WaitForAssertion(() => Assert.Equal(2, cut.FindAll("[data-abom-ast]").Count));
        Assert.Contains("CodeQL 2.27.1", cut.Find("[data-abom-ast='9']").TextContent);
        Assert.Contains("CodeQL not recorded", cut.Find("[data-abom-ast='4']").TextContent);
    }
}
