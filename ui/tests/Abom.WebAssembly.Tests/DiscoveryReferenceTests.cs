using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A dependency row names a version, not a package. The queue is keyed (node_key, ref), so
/// fourteen rows reading "actions/checkout" are fourteen different versions and the grid has to
/// say which.
/// </summary>
public class DiscoveryReferenceTests : AbomTestContext
{
    private void Serve(string frontier)
    {
        Answer("api/discovery/suites", """
            {"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}
            """);
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().ToJson());
        Answer("api/frontier", frontier);
    }

    private const string Pinned = """
        [{"nodeKey":"actions/checkout","ref":"v4","owner":"actions","repo":"checkout",
          "status":"pending","depth":1,"score":9,"attempts":0,"analysisCount":0,
          "resolvedSha":"","discoveredFrom":"octo/app","reason":"","claimedAt":"",
          "nextAttemptAt":"","supplyChain":"runtime","analyzedAt":"","tier":"direct"}]
        """;

    // Without this the grid draws the package name, and every version of it collapses into rows
    // that differ only in a trailing "named by". Asserted on rendered text rather than markup:
    // the reference is styled in its own span, and what matters is what a reader sees.
    [Fact]
    public void ARowNamesTheVersionItAnalyzes()
    {
        Serve(Pinned);

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("actions/checkout",
            cut.Find(".abom-grid").TextContent));
        Assert.Contains("actions/checkout@v4",
            cut.Find(".abom-grid").TextContent.Replace("\n", "").Replace(" ", ""));
    }

    // An empty ref means the default branch, which is a real answer. 1,253 rows on the corpus
    // have one, so printing the bare name for them re-creates the bug for half the queue.
    [Fact]
    public void ARowWithNoReferenceSaysItIsTheDefaultBranch()
    {
        Serve("""
            [{"nodeKey":"actions/cache","ref":"","owner":"actions","repo":"cache",
              "status":"pending","depth":1,"score":9,"attempts":0,"analysisCount":0,
              "resolvedSha":"","discoveredFrom":"octo/app","reason":"","claimedAt":"",
              "nextAttemptAt":"","supplyChain":"runtime","analyzedAt":"","tier":"direct"}]
            """);

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("actions/cache", cut.Markup));
        Assert.Contains("default branch", cut.Markup);
    }

    // "Note" described the column when it held one first-discoverer. It names who runs this
    // version, which is a different question and the one an operator acts on.
    [Fact]
    public void TheColumnOfNamingRepositoriesIsCalledUsedBy()
    {
        Serve(Pinned);

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("Used by", cut.Markup));
        Assert.DoesNotContain(">Note<", cut.Markup);
    }

    // 220 repositories name one version of actions/checkout, so the list is collapsed and the
    // count carries the weight. The count is exact even though the names are a preview.
    [Fact]
    public void UsedByStatesTheTotalAndCollapsesThePreview()
    {
        Serve("""
            [{"nodeKey":"actions/checkout","ref":"v4","owner":"actions","repo":"checkout",
              "status":"pending","depth":1,"score":9,"attempts":0,"analysisCount":0,
              "resolvedSha":"","discoveredFrom":"octo/app","reason":"","claimedAt":"",
              "nextAttemptAt":"","supplyChain":"runtime","analyzedAt":"","tier":"direct",
              "usedBy":["octo/app","octo/tools"],"usedByCount":220,"usedByOwned":12}]
            """);

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("220 repositories", cut.Markup));
        Assert.Contains("octo/tools", cut.Markup);
        Assert.Contains("and 218 more", cut.Markup);
        // What the operator can change themselves, which is the number they act on.
        Assert.Contains("12 yours", cut.Markup);
        // Collapsed by default: the names are inside a closed disclosure, not laid out in the row.
        Assert.DoesNotContain("<details class=\"abom-usedby\" open", cut.Markup);
    }

    // With no verified account the owned count is not zero, it is unknown, and printing "0 yours"
    // would answer a question nothing has asked.
    [Fact]
    public void NoVerifiedAccountMeansNoOwnershipClaimAtAll()
    {
        Serve("""
            [{"nodeKey":"actions/checkout","ref":"v4","owner":"actions","repo":"checkout",
              "status":"pending","depth":1,"score":9,"attempts":0,"analysisCount":0,
              "resolvedSha":"","discoveredFrom":"octo/app","reason":"","claimedAt":"",
              "nextAttemptAt":"","supplyChain":"runtime","analyzedAt":"","tier":"direct",
              "usedBy":["octo/app"],"usedByCount":220,"usedByOwned":0}]
            """);

        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("220 repositories", cut.Markup));
        Assert.DoesNotContain("0 yours", cut.Markup);
    }
}
