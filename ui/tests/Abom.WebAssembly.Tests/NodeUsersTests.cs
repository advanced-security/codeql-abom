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
         "isExternal":true,"analyzed":true,"dependentRepos":2,
         "rules":[],"uses":[]}
        """;

    // v4 first, so the first version's list is the one each test opens.
    private const string Versions = """
        {"key":"actions/checkout","total":2,"advisories":[],
         "items":[
           {"reference":"v4","commit":"","queue":"analyzed","lockedTo":[],"direct":3,"transitive":0,
            "lines":9,"findings":null},
           {"reference":"v3","commit":"","queue":"analyzed","lockedTo":[],"direct":1,"transitive":0,
            "lines":1,"findings":null}]}
        """;

    private void ServeNode()
    {
        Answer("/-/versions", Versions);
        Answer("/-/uses", """{"key":"actions/checkout","total":0,"items":[]}""");
        Answer("api/node/", Detail);
    }

    private void Serve(string users)
    {
        Answer("/-/users", users);
        ServeNode();
    }

    private IRenderedComponent<Pages.NodePage> RenderNode() =>
        Render<Pages.NodePage>(p => p.Add(c => c.Key, "actions/checkout"));

    // The list opens from a version's row, once the versions have arrived.
    private static void OpenUsers(IRenderedComponent<Pages.NodePage> cut) =>
        cut.WaitForAssertion(() => cut.Find(".abom-ref-users").Click());

    // The whole point of the entry: a reference the grid could only count is now a reference
    // whose users can be read — the starting points, which are the ones somebody here can change.
    [Fact]
    public void EveryStartingPointNamingAVersionCanBeRead()
    {
        Serve("""
            {"key":"actions/checkout","ref":"v4","total":3,"seeds":2,"owned":0,"ownedKnown":false,
             "truncated":false,
             "repos":[{"fullName":"octo-org/app","origin":"seed","owned":false,"uses":2},
                      {"fullName":"octo-org/tool","origin":"seed","owned":false,"uses":1},
                      {"fullName":"other-org/lib","origin":"upstream","owned":false,"uses":1}]}
            """);

        var cut = RenderNode();
        OpenUsers(cut);

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-user='octo-org/app']")));
        Assert.NotEmpty(cut.FindAll("[data-abom-user='octo-org/tool']"));
    }

    // Upstream repositories run the version but are not anyone's here to change, so they are not
    // listed; the list says how many it left out rather than reading as complete.
    [Fact]
    public void AnUpstreamUserIsLeftOutAndCounted()
    {
        Serve("""
            {"key":"actions/checkout","ref":"v4","total":2,"seeds":1,"owned":0,"ownedKnown":false,
             "truncated":false,
             "repos":[{"fullName":"octo-org/app","origin":"seed","owned":false,"uses":1},
                      {"fullName":"other-org/lib","origin":"upstream","owned":false,"uses":1}]}
            """);

        var cut = RenderNode();
        OpenUsers(cut);

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-user='octo-org/app']")));
        Assert.Empty(cut.FindAll("[data-abom-user='other-org/lib']"));
        Assert.Contains("1 other repository names it too", cut.Find("[data-abom-ref-users-of='v4']").TextContent);
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
        OpenUsers(cut);

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
        OpenUsers(cut);

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
        OpenUsers(cut);

        cut.WaitForAssertion(() => Assert.Contains("more not shown", cut.Markup));
    }

    // A request in flight is not an empty answer. Rendering "nobody uses this" while the list is
    // loading is the regression this repository has already shipped once.
    [Fact]
    public void TheListDoesNotClaimNobodyUsesItWhileLoading()
    {
        ServeNode();
        Withhold("/-/users");

        var cut = RenderNode();
        OpenUsers(cut);

        cut.WaitForAssertion(() => Assert.Contains("Loading the repositories naming", cut.Markup));
        Assert.DoesNotContain("No repository names", cut.Markup);
    }
}
