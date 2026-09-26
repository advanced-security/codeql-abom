using Abom.WebAssembly.Models;
using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The hovered node's metadata is typeset in the key panel rather than painted into the canvas.
///
/// <para>On the canvas it was the largest box on screen, and it covered the very neighbors a
/// reader hovers a node in order to compare it against. In the panel it costs no pixels over the
/// graph and it outlives the pointer, so a reader can move towards what they are reading.</para>
///
/// <para>These pin the two things a screenshot cannot check: that an unmeasured fact says so
/// rather than reporting zero, and that the panel ties itself back to the kind it is describing.
/// </para>
/// </summary>
public sealed class ExplorerNodePanelTests : AbomTestContext
{
    public ExplorerNodePanelTests()
    {
        Answer("api/graph/generation", "{\"generation\":0}");
        Answer("api/summary", "{}");
        ArriveWithAScope();
    }

    private static GraphNodeBrief Brief(string key) => new()
    {
        Key = key,
        Kind = "workflow",
        Repository = "octo/app",
        Origin = "seed",
        Path = ".github/workflows/ci.yml",
        Grouping = "payments",
        DependentRepos = 12,
        ReachKnown = true,
        Findings = 3,
        RefsInUse = 4,
        RefsAnalyzed = 4,
    };

    [Fact]
    public async Task HoveringANodeTypesetsItsMetadataBesideTheGraph()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeHovered(
            Brief("octo/app/.github/workflows/ci.yml")));

        Assert.Contains("octo/app/.github/workflows/ci.yml", page.Markup);
        Assert.Contains("payments", page.Markup);
        Assert.Contains("12", page.Markup);
    }

    /// <summary>
    /// The panel is a place for a fact, not a shape that has to be there. Before anything has been
    /// hovered there is nothing to say, and reserving the room would push the key down for a reader
    /// who has not asked a question yet.
    /// </summary>
    [Fact]
    public void NothingHoveredMeansNoPanel()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        Assert.DoesNotContain("abom-nodecard", page.Markup);
    }

    /// <summary>
    /// Absent is not zero. A node ingested since reach was last rebuilt has no measurement, and
    /// printing "0 repositories" asserts one nobody made.
    /// </summary>
    [Fact]
    public async Task AnUnmeasuredReachSaysSoRatherThanReportingNone()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeHovered(new GraphNodeBrief
        {
            Key = "actions/checkout@v4",
            Kind = "action",
            Owner = "actions",
            ReachKnown = false,
            DependentRepos = 0,
        }));

        Assert.Contains("not measured", page.Markup);
        Assert.DoesNotContain("0 repositories", page.Markup);
    }

    /// <summary>
    /// A dependency nothing ever queued has no coverage to report, and "0 of 0 references" reads
    /// as a measurement of a thing that was looked at.
    /// </summary>
    [Fact]
    public async Task ADependencyNothingQueuedIsNotReportedAsAnalyzedNone()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeHovered(new GraphNodeBrief
        {
            Key = "actions/never-seen",
            Kind = "action",
            // Coverage is a claim about a dependency. The payload marks one, and a node that is
            // not one never had references in use to report.
            IsExternal = true,
            RefsInUse = 0,
            RefsAnalyzed = 0,
        }));

        Assert.Contains("never queued", page.Markup);
        Assert.DoesNotContain("0 of 0", page.Markup);
    }

    /// <summary>
    /// The panel names a kind, and the key is where a reader already learned what that kind looks
    /// like. Marking the row is what ties the two together without a second legend.
    /// </summary>
    [Fact]
    public async Task TheKeyMarksTheKindThePanelIsDescribing()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeHovered(Brief("octo/app/.github/workflows/ci.yml")));

        Assert.Contains("abom-key-row-shown", page.Markup);
    }

    /// <summary>
    /// Dismissing is the reader's, not the pointer's. The panel stays put when the pointer leaves
    /// the node, so there has to be a way to put it away deliberately.
    /// </summary>
    [Fact]
    public async Task ThePanelCanBeDismissed()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeHovered(Brief("octo/app")));
        Assert.Contains("abom-nodecard", page.Markup);

        await page.InvokeAsync(() => page.Instance.ClearNodeDetail());
        Assert.DoesNotContain("abom-nodecard", page.Markup);
    }

    /// <summary>
    /// An account is not a dependency, so it has no analysis coverage to report. "Never queued" is
    /// a true sentence about the wrong kind of thing, and it reads as a gap somebody should close.
    /// </summary>
    [Fact]
    public async Task AnAccountIsNotReportedAsADependencyNobodyAnalyzed()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeHovered(new GraphNodeBrief
        {
            Key = "org:actions",
            Kind = "org",
            Owner = "actions",
            IsExternal = true,
            DependentRepos = 89,
            ReachKnown = true,
        }));

        Assert.DoesNotContain("never queued", page.Markup);
        // The heading is the account, not the synthetic key the graph addresses it by.
        Assert.Contains(">actions<", page.Markup);
        Assert.DoesNotContain(">org:actions<", page.Markup);
    }

    /// <summary>
    /// A commit-pinned reference is forty hex characters of a name that is already long, and the
    /// key line below repeats it verbatim. Three copies of one sha is the whole panel.
    /// </summary>
    [Fact]
    public async Task ACommitPinnedNameIsAbbreviatedWhereTheKeyAlreadyCarriesIt()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeHovered(new GraphNodeBrief
        {
            Key = "apache/skywalking-eyes/dependency@e1a02359b239bd28de3f6d35fdc870250fa513d5",
            Kind = "action",
            Owner = "apache",
            Ref = "e1a02359b239bd28de3f6d35fdc870250fa513d5",
        }));

        Assert.Contains("dependency@e1a0235<", page.Markup);
        Assert.Contains("<dd>e1a0235</dd>", page.Markup);
        // The key line keeps the whole thing, so the reader can still copy it.
        Assert.Contains(
            "abom-node-key\">apache/skywalking-eyes/dependency@e1a02359b239bd28de3f6d35fdc870250fa513d5<",
            page.Markup);
    }

    /// <summary>
    /// How severe a node's findings are is the half of blast radius a reader cannot see from its
    /// size. Each facet carries the band the server read its score as, and a rule declaring no
    /// score is unknown rather than the level its result reported.
    /// </summary>
    [Fact]
    public async Task ANodesFindingsAreSummarizedBySeverity()
    {
        StubGraphModule();
        var page = Render<Explorer>();

        await page.InvokeAsync(() => page.Instance.OnNodeHovered(new GraphNodeBrief
        {
            Key = "octo/app/.github/workflows/ci.yml",
            Kind = "workflow",
            Findings = 5,
            Facets =
            [
                new FindingFacet { Rule = "actions/code-injection/critical", Severity = "error", SecuritySeverity = 9.3, Band = "critical", Count = 1 },
                new FindingFacet { Rule = "actions/unpinned-tag", Severity = "warning", SecuritySeverity = 5.0, Band = "medium", Count = 3 },
                new FindingFacet { Rule = "actions/unnecessary-use-of-advanced-config", Severity = "recommendation", Band = "unknown", Count = 1 },
            ],
        }));

        var severity = page.Find("[data-abom-node-fact='severity']").TextContent;
        Assert.Contains("1 critical", severity);
        Assert.Contains("3 medium", severity);
        Assert.Contains("1 unknown", severity);
        Assert.True(severity.IndexOf("critical", StringComparison.Ordinal)
                    < severity.IndexOf("medium", StringComparison.Ordinal),
            "the most severe is not named first");
    }

    /// Every typed call needs a result: loose mode answers `default`, which for a record is null,
    /// and the page dereferences what the module hands back.
    private void StubGraphModule()
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(new Explorer.Counts(0, 0, 0, 0));
        module.SetupVoid("setPalette", _ => true).SetVoidResult();
        module.SetupVoid("setAnimate", _ => true).SetVoidResult();
        module.SetupVoid("setGroupBy", _ => true).SetVoidResult();
        module.SetupVoid("setCollapsed", _ => true).SetVoidResult();
        module.SetupVoid("setView", _ => true).SetVoidResult();
        module.SetupVoid("setGeneration", _ => true).SetVoidResult();
        module.SetupVoid("setSelection", _ => true).SetVoidResult();
        module.SetupVoid("resize", _ => true).SetVoidResult();
        module.SetupVoid("rebuildNodes", _ => true).SetVoidResult();
    }
}
