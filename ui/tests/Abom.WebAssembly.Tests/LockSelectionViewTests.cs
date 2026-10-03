using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A dependency a lock selects is analyzed at the commit the lock names, beside the ref the
/// workflow declares. These pin what the interface says about the two: which commit runs, whose
/// lock chose it, and what is simply not known.
/// </summary>
public class LockSelectionViewTests : AbomTestContext
{
    // Two public consumers' locks of dtolnay/rust-toolchain@v1, measured 2026-09-27.
    private const string CleaveLocked = "e97e2d8cc328f1b50210efc529dca0028893a2d9";
    private const string BitLocked = "6c977a6ca4077a0ceb28ffbe03f59d46e9ac8772";

    // The one reference the five consumers write, and the commits their locks select behind it.
    private static string Detail(string locked) => $$"""
        {"key":"dtolnay/rust-toolchain","total":1,"advisories":[],
         "items":[{"reference":"v1","commit":"","queue":"analyzed","lockedTo":[{{locked}}],
                   "direct":5,"transitive":0,"lines":5,"findings":null}]}
        """;

    private void ServeNode(string versions, string users = """{"key":"","ref":"","repos":[]}""")
    {
        Answer("/-/users", users);
        Answer("/-/versions", versions);
        Answer("/-/uses", """{"key":"dtolnay/rust-toolchain","total":0,"items":[]}""");
        Answer("api/node/", """
            {"key":"dtolnay/rust-toolchain","kind":"action","repository":"","origin":"","path":"",
             "isExternal":true,"analyzed":true,"dependentRepos":5,
             "rules":[],"uses":[]}
            """);
    }

    private IRenderedComponent<Pages.NodePage> RenderNode() =>
        Render<Pages.NodePage>(p => p.Add(c => c.Key, "dtolnay/rust-toolchain"));

    private static void OpenUsers(IRenderedComponent<Pages.NodePage> cut) =>
        cut.WaitForAssertion(() => cut.Find(".abom-ref-users").Click());

    // What the source says and what runs are both on the row, and the declared counts do not move.
    [Fact]
    public void ADeclaredRefSaysWhichCommitsLocksSelectBehindIt()
    {
        ServeNode(Detail($$"""
            {"commit":"{{BitLocked}}","repositories":1},{"commit":"{{CleaveLocked}}","repositories":1}
            """));

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-ref-locked]")));
        var locked = cut.Find("[data-abom-ref-locked]").TextContent;
        Assert.Contains(BitLocked[..12], locked);
        Assert.Contains(CleaveLocked[..12], locked);
        Assert.Contains(">5<", cut.Markup);
    }

    // Nothing locked behind a ref is a quiet cell, not a claim: whether each user's lock was read
    // is said per repository, where it can be.
    [Fact]
    public void ARefNothingLocksCarriesNoLockClaim()
    {
        ServeNode(Detail(""));

        var cut = RenderNode();

        cut.WaitForAssertion(() => Assert.Contains("v1", cut.Markup));
        Assert.Empty(cut.FindAll("[data-abom-ref-locked]"));
    }

    // Each repository says what its lock did with this version: selected another commit through
    // named workflows, or had no lockfile, or had one that could not be read, or was never read —
    // four answers that "not locked" would collapse into one.
    [Fact]
    public void EachRepositorySaysWhatItsLockSelectedOrWhyNothing()
    {
        ServeNode(Detail(""), $$"""
            {"key":"dtolnay/rust-toolchain","ref":"v1","total":5,"seeds":5,"owned":0,
             "ownedKnown":false,"truncated":false,
             "repos":[
               {"fullName":"octo/cleave","origin":"seed","owned":false,"uses":2,
                "locks":[{"commit":"{{CleaveLocked}}","consumer":"octo/cleave",
                          "workflows":[".github/workflows/kernel.yml"]}],
                "lockedUses":1,"lockCoverage":["covered"]},
               {"fullName":"octo/unlocked","origin":"seed","owned":false,"uses":1,
                "locks":[],"lockedUses":0,"lockCoverage":["not_onboarded"]},
               {"fullName":"octo/broken","origin":"seed","owned":false,"uses":1,
                "locks":[],"lockedUses":0,"lockCoverage":["unsupported"]},
               {"fullName":"octo/partial","origin":"seed","owned":false,"uses":1,
                "locks":[],"lockedUses":0,"lockCoverage":["covered"]},
               {"fullName":"octo/plain","origin":"seed","owned":false,"uses":1,
                "locks":[],"lockedUses":0,"lockCoverage":[]}]}
            """);

        var cut = RenderNode();
        OpenUsers(cut);

        cut.WaitForAssertion(() => Assert.Contains("octo/cleave", cut.Markup));
        string LockOf(string repo) =>
            cut.Find($"[data-abom-user='{repo}'] [data-abom-user-lock]").TextContent;
        Assert.Contains($"locked to {CleaveLocked[..12]}", LockOf("octo/cleave"));
        Assert.Contains("kernel.yml", LockOf("octo/cleave"));
        Assert.Contains("1 of 2 uses", LockOf("octo/cleave"));
        Assert.Contains("no lockfile", LockOf("octo/unlocked"));
        Assert.Contains("lockfile unsupported", LockOf("octo/broken"));
        Assert.Contains("not in its lock", LockOf("octo/partial"));
        Assert.Contains("lockfile not read", LockOf("octo/plain"));
    }

    // A caller's lock governs a composite's step for that caller only, so it is the caller's
    // choice, named with the pin it reached the composite through.
    [Fact]
    public void ACallersLockIsNamedAsTheCallersChoice()
    {
        const string python = "89abcdef0123456789abcdef0123456789abcdef";
        ServeNode(Detail(""), $$"""
            {"key":"actions/setup-python","ref":"v5","total":1,"seeds":1,"owned":0,
             "ownedKnown":false,"truncated":false,
             "repos":[{"fullName":"octo/tool","origin":"seed","owned":false,"uses":1,
                       "locks":[{"commit":"{{python}}","consumer":"octo/cleave","workflows":[],
                                 "via":"octo/tool@v1"}],
                       "lockedUses":0,"lockCoverage":[]}]}
            """);

        var cut = RenderNode();
        OpenUsers(cut);

        cut.WaitForAssertion(() => Assert.Contains("octo/tool", cut.Markup));
        var text = cut.Find("[data-abom-user='octo/tool'] [data-abom-user-lock]").TextContent;
        Assert.Contains($"octo/cleave's lock selects {python[..12]}", text);
        Assert.Contains("through octo/tool@v1", text);
        Assert.DoesNotContain("locked to", text);
    }

    private void ServeCoverage(string frontier)
    {
        Answer("api/discovery/suites", """{"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}""");
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().ToJson());
        Answer("api/frontier", frontier);
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/actions", """{"items":[],"total":0}""");
    }

    private IRenderedComponent<Pages.Inventory> Coverage()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-unit='coverage']")));
        page.Find("[data-abom-unit='coverage']").Click();
        return page;
    }

    private static string Row(string reference, string lockedBy = "", int count = 0) => $$"""
        {"nodeKey":"dtolnay/rust-toolchain","ref":"{{reference}}","owner":"dtolnay",
         "repo":"rust-toolchain","status":"done","depth":1,"score":1,"attempts":0,
         "analysisCount":1,"resolvedSha":"","discoveredFrom":"octo/cleave","reason":"",
         "claimedAt":"","nextAttemptAt":"","supplyChain":"runtime",
         "analyzedAt":"2026-09-27T00:00:00Z","tier":"direct"{{lockedBy}}}
        """;

    // One dependency at a declared ref and at the commit a lock selects is two rows. Without the
    // version on each they read as a duplicate, and without whose lock the second reads as used by
    // nobody.
    [Fact]
    public void AQueueRowSaysWhichVersionItIsAndWhoseLockChoseIt()
    {
        ServeCoverage("[" + Row("v1") + "," +
            Row(CleaveLocked, ""","lockedBy":["octo/cleave"],"lockedByCount":1""") + "," +
            Row(BitLocked, ""","lockedBy":["octo/bitfuckit","octo/a","octo/b"],"lockedByCount":7""") +
            "," + Row("") + "]");

        var cut = Coverage();

        cut.WaitForAssertion(() => Assert.Equal(4, cut.FindAll("[data-abom-coverage-version]").Count));
        var versions = cut.FindAll("[data-abom-coverage-version]").Select(v => v.TextContent).ToList();
        Assert.Contains(versions, v => v.Contains("@v1") && !v.Contains("locked by"));
        Assert.Contains(versions, v => v.Contains("@" + CleaveLocked[..12]) && v.Contains("locked by octo/cleave"));
        Assert.Contains(versions, v => v.Contains("locked by octo/bitfuckit") && v.Contains("6 more"));
        Assert.Contains(versions, v => v.Contains("default branch"));
    }

    // A relationship is one line of one workflow, and says exactly what its lock selects for it:
    // the repository's own lock through the workflow named, or a caller's through its pin.
    [Fact]
    public void ARelationshipSaysWhatALockSelectsForIt()
    {
        const string python = "89abcdef0123456789abcdef0123456789abcdef";
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/actions", """{"items":[],"total":0}""");
        Answer("api/relationships", $$"""
            {"items":[
              {"edgeKey":"octo/cleave:kernel:10","kind":"step-uses-action",
               "source":"step:octo/cleave:kernel.yml::0","target":"dtolnay/rust-toolchain",
               "ref":"v1","refStyle":"tag","boundary":"cross-owner","repository":"octo/cleave",
               "filePath":".github/workflows/kernel.yml","startLine":10,"findings":0,"rules":[],
               "locked":[{"commit":"{{CleaveLocked}}","consumer":"octo/cleave",
                          "workflow":".github/workflows/kernel.yml"}]},
              {"edgeKey":"octo/tool:action:7","kind":"composite-step-uses-action",
               "source":"step:octo/tool:action.yml::0","target":"actions/setup-python",
               "ref":"v5","refStyle":"tag","boundary":"cross-owner","repository":"octo/tool",
               "filePath":"action.yml","startLine":7,"findings":0,"rules":[],
               "locked":[{"commit":"{{python}}","consumer":"octo/cleave","via":"octo/tool@v1"}]},
              {"edgeKey":"octo/plain:kernel:10","kind":"step-uses-action",
               "source":"step:octo/plain:kernel.yml::0","target":"dtolnay/rust-toolchain",
               "ref":"v1","refStyle":"tag","boundary":"cross-owner","repository":"octo/plain",
               "filePath":".github/workflows/kernel.yml","startLine":10,"findings":0,"rules":[],
               "locked":[]}],"total":3}
            """);

        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-unit='relationships']")));
        page.Find("[data-abom-unit='relationships']").Click();

        page.WaitForAssertion(() => Assert.Equal(2, page.FindAll("[data-abom-relationship-locked]").Count));
        var locks = page.FindAll("[data-abom-relationship-locked]").Select(e => e.TextContent).ToList();
        Assert.Contains(locks, t => t.Contains($"locked to {CleaveLocked[..12]}") && t.Contains("kernel.yml"));
        Assert.Contains(locks, t => t.Contains($"octo/cleave's lock selects {python[..12]}") && t.Contains("octo/tool@v1"));
    }

    private const string VerifiedUsers = $$$"""
        {"key":"dtolnay/rust-toolchain","ref":"v1","total":2,"seeds":2,"owned":0,
         "ownedKnown":false,"truncated":false,
         "repos":[
           {"fullName":"octo/cleave","origin":"seed","owned":false,"uses":1,
            "locks":[{"commit":"{{{CleaveLocked}}}","consumer":"octo/cleave",
                      "workflows":[".github/workflows/kernel.yml"]}],
            "lockedUses":1,"lockCoverage":["covered"],
            "verification":{"mode":"network","outcome":"failed","findings":11,
                            "verifiedAt":"2026-09-28T05:37:23Z"}},
           {"fullName":"octo/plain","origin":"seed","owned":false,"uses":1,
            "locks":[],"lockedUses":0,"lockCoverage":["not_onboarded"]}]}
        """;

    // What GitHub's own verification said is shown beside ABOM's reading of the lock, named as the
    // tool's answer and dated; a repository nothing verified shows no verification at all.
    [Fact]
    public void TheNewestVerificationReadsBesideTheLock()
    {
        ServeNode(Detail(""), VerifiedUsers);

        var cut = RenderNode();
        OpenUsers(cut);

        cut.WaitForAssertion(() => Assert.Contains("octo/cleave", cut.Markup));
        var cleave = cut.Find("[data-abom-user='octo/cleave'] [data-abom-user-verification]").TextContent;
        Assert.Contains("gh actions-lock (network)", cleave);
        Assert.Contains("failed", cleave);
        Assert.Contains("11 findings", cleave);
        Assert.Empty(cut.FindAll("[data-abom-user='octo/plain'] [data-abom-user-verification]"));
    }

    // A check is offered only where there is a lockfile to check, runs only when pressed, and
    // re-reads the list once it has an answer.
    [Fact]
    public void AVerificationRunsOnlyWhenPressedAndOnlyWhereThereIsALockfile()
    {
        ServeNode(Detail(""), VerifiedUsers);
        Answer("api/locks/verify", """{"repository":"octo/cleave","commit":"c1","mode":"local","outcome":"passed","findings":[]}""");

        var cut = RenderNode();
        OpenUsers(cut);
        cut.WaitForAssertion(() => Assert.Contains("octo/cleave", cut.Markup));

        Assert.DoesNotContain(Sent, r => r.Url.Contains("api/locks/verify"));
        Assert.Empty(cut.FindAll("[data-abom-user='octo/plain'] [data-abom-verify-lock]"));
        var usersBefore = Sent.Count(r => r.Url.Contains("/-/users"));

        cut.Find("[data-abom-user='octo/cleave'] [data-abom-verify-lock]").Click();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/locks/verify")));
        cut.WaitForAssertion(() => Assert.True(Sent.Count(r => r.Url.Contains("/-/users")) > usersBefore));
    }
}
