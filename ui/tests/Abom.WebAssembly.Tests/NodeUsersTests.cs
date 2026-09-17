using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Discovery grid previews five names and dead-ends at "and N more", on 291 of the corpus's
/// 5,787 name-and-version pairs. These pin what the rest of that list is allowed to claim once
/// the node page shows it.
/// </summary>
public class NodeUsersTests : AbomTestContext
{
    private const string Detail = """
        {"key":"actions/checkout","kind":"action","repository":"","origin":"","path":"",
         "isExternal":true,"analyzed":true,"dependentRepos":2,"directDependents":3,
         "refs":[{"ref":"v4","count":9,"repositories":3},{"ref":"v3","count":1,"repositories":1}],
         "rules":[],"advisories":[],"usedBy":[],"uses":[]}
        """;

    private void Serve(string users)
    {
        Answer("/-/users", users);
        Answer("/-/reach", "[]");
        Answer("/-/impact", """{"key":"actions/checkout","direct":0,"transitive":0,"repos":[],"unanalyzed":0}""");
        Answer("api/node/", Detail);
    }

    private IRenderedComponent<Pages.NodePage> RenderNode() =>
        Render<Pages.NodePage>(p => p.Add(c => c.Key, "actions/checkout"));

    // The whole point of the entry: a reference the grid could only count is now a reference
    // whose users can be read.
    [Fact]
    public void EveryRepositoryNamingAVersionCanBeRead()
    {
        Serve("""
            {"key":"actions/checkout","ref":"v4","total":3,"seeds":2,"owned":0,"ownedKnown":false,
             "truncated":false,
             "repos":[{"fullName":"octo-org/app","origin":"seed","owned":false,"uses":2},
                      {"fullName":"octo-org/tool","origin":"seed","owned":false,"uses":1},
                      {"fullName":"other-org/lib","origin":"upstream","owned":false,"uses":1}]}
            """);

        var cut = RenderNode();
        cut.Find(".abom-ref-users").Click();

        cut.WaitForAssertion(() => Assert.Contains("octo-org/app", cut.Markup));
        Assert.Contains("octo-org/tool", cut.Markup);
        Assert.Contains("other-org/lib", cut.Markup);
    }

    // Upstream repositories run the version but are not anyone's here to change. Listing them
    // without saying which they are would read as three repositories to go and fix.
    [Fact]
    public void AnUpstreamUserIsMarkedAsOutOfScope()
    {
        Serve("""
            {"key":"actions/checkout","ref":"v4","total":2,"seeds":1,"owned":0,"ownedKnown":false,
             "truncated":false,
             "repos":[{"fullName":"octo-org/app","origin":"seed","owned":false,"uses":1},
                      {"fullName":"other-org/lib","origin":"upstream","owned":false,"uses":1}]}
            """);

        var cut = RenderNode();
        cut.Find(".abom-ref-users").Click();

        cut.WaitForAssertion(() => Assert.Contains("upstream", cut.Markup));
    }

    // With nothing verified, "0 yours" is a question nobody has answered rather than a count of
    // none — the same rule the Discovery label and the pinning split already follow.
    [Fact]
    public void OwnershipIsNotClaimedWithNoVerifiedAccount()
    {
        Serve("""
            {"key":"actions/checkout","ref":"v4","total":1,"seeds":1,"owned":0,"ownedKnown":false,
             "truncated":false,
             "repos":[{"fullName":"octo-org/app","origin":"seed","owned":false,"uses":1}]}
            """);

        var cut = RenderNode();
        cut.Find(".abom-ref-users").Click();

        cut.WaitForAssertion(() => Assert.Contains("octo-org/app", cut.Markup));
        Assert.DoesNotContain("0 yours", cut.Markup);
    }

    [Fact]
    public void OwnershipIsShownOnceAnAccountIsVerified()
    {
        Serve("""
            {"key":"actions/checkout","ref":"v4","total":2,"seeds":2,"owned":1,"ownedKnown":true,
             "truncated":false,
             "repos":[{"fullName":"octo-org/app","origin":"seed","owned":true,"uses":1},
                      {"fullName":"other-org/lib","origin":"seed","owned":false,"uses":1}]}
            """);

        var cut = RenderNode();
        cut.Find(".abom-ref-users").Click();

        cut.WaitForAssertion(() => Assert.Contains("1 yours", cut.Markup));
    }

    // A list that was cut and does not say so is a list that looks complete. This is the case
    // the budget exists for.
    [Fact]
    public void ATruncatedListSaysSoRatherThanLookingComplete()
    {
        Serve("""
            {"key":"actions/checkout","ref":"v4","total":2000,"seeds":2000,"owned":0,
             "ownedKnown":false,"truncated":true,
             "repos":[{"fullName":"octo-org/app","origin":"seed","owned":false,"uses":1}]}
            """);

        var cut = RenderNode();
        cut.Find(".abom-ref-users").Click();

        cut.WaitForAssertion(() => Assert.Contains("more not shown", cut.Markup));
    }

    // A request in flight is not an empty answer. Rendering "nobody uses this" while the list is
    // loading is the regression this repository has already shipped once.
    [Fact]
    public void TheListDoesNotClaimNobodyUsesItWhileLoading()
    {
        Answer("/-/reach", "[]");
        Answer("/-/impact", """{"key":"actions/checkout","direct":0,"transitive":0,"repos":[],"unanalyzed":0}""");
        Answer("api/node/", Detail);
        Withhold("/-/users");

        var cut = RenderNode();
        cut.Find(".abom-ref-users").Click();

        cut.WaitForAssertion(() => Assert.Contains("abom-ref-users", cut.Markup));
        Assert.DoesNotContain("No repository names", cut.Markup);
    }
}
