using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Whose a thing is reads the same everywhere it is shown: owned where the declaration covers its
/// repository, a starting point where it was seeded and nobody declared it, then upstream and
/// external.
///
/// <para>The Origin column said "starting point" for everything seeded, which is provenance: on the
/// live store 2 of 26 seeded composite actions and 31 of 153 seeded workflows sit in repositories
/// nobody declared, and calling them owned would have claimed them.</para>
/// </summary>
public class OriginWordsTests : AbomTestContext
{
    private const string Rows = """
        {"items":[
          {"nodeKey":"octo/app/.github/actions/build","kind":"composite_action","owner":"octo","repo":"app",
           "origin":"seed","owned":true,"dependentRepos":1,"dependentSteps":1,"distinctRefs":0,"findings":0,
           "advisories":0,"analyzed":false,"refs":[]},
          {"nodeKey":"docker/build-push-action/action","kind":"composite_action","owner":"docker",
           "repo":"build-push-action","origin":"seed","owned":false,"dependentRepos":1,"dependentSteps":1,
           "distinctRefs":0,"findings":0,"advisories":0,"analyzed":true,"refs":[]},
          {"nodeKey":"apache/skywalking-eyes","kind":"composite_action","owner":"apache","repo":"skywalking-eyes",
           "origin":"upstream","owned":false,"dependentRepos":1,"dependentSteps":5,"distinctRefs":2,"findings":0,
           "advisories":0,"analyzed":true,"refs":["main"]},
          {"nodeKey":"actions/checkout","kind":"action","owner":"actions","repo":"checkout","origin":"",
           "owned":false,"dependentRepos":21,"dependentSteps":269,"distinctRefs":9,"findings":0,
           "advisories":0,"analyzed":true,"refs":["v4"]}],
         "total":4}
        """;

    private const string Workflow = "octo/app/.github/workflows/ci.yml";

    private static string Detail(bool owned) => $$"""
        {"key":"{{Workflow}}","kind":"workflow","repository":"octo/app","origin":"seed","owned":{{(owned ? "true" : "false")}},
         "path":".github/workflows/ci.yml","isExternal":false,"analyzed":true,"refsInUse":0,"refsAnalyzed":0,
         "dependentRepos":0,"rules":[],"uses":[]}
        """;

    [Fact]
    public void TheOriginColumnSaysOwnedOnlyWhereTheDeclarationCoversTheRepository()
    {
        Answer("api/actions", Rows);

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            foreach (var (key, word) in new[]
                     {
                         ("octo/app/.github/actions/build", "owned"),
                         ("docker/build-push-action/action", "starting point"),
                         ("apache/skywalking-eyes", "upstream"),
                         ("actions/checkout", "external"),
                     })
            {
                Assert.Equal(word, page.Find($"[data-abom-origin='{key}']").TextContent.Trim());
            }
        });
    }

    [Theory]
    [InlineData(true, "owned")]
    [InlineData(false, "starting point")]
    public void TheDetailsPageSaysOwnedOnlyWhereTheDeclarationCoversTheRepository(bool owned, string word)
    {
        Answer("/-/versions", $$"""{"key":"{{Workflow}}","total":0,"items":[],"advisories":[]}""");
        Answer("/-/uses", $$"""{"key":"{{Workflow}}","total":0,"hiddenUpstream":0,"unanalyzed":0,"items":[]}""");
        Answer("api/node/", Detail(owned));
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo($"/node/{Workflow}");

        var page = Render<Pages.NodePage>(p => p.Add(c => c.Key, Workflow));

        page.WaitForAssertion(() =>
            Assert.Equal(word, page.Find("[data-abom-node-origin]").TextContent.Trim()));
    }
}
