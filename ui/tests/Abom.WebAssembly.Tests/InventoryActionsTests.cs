using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A unit's actions are its own, in the same way its columns are.
///
/// <para>An action offered against a row it cannot apply to is the defect a structurally-zero
/// column was, arriving through the toolbar instead of through the grid. The two are not the same
/// question either: a bill of materials describes the <em>scope</em> and is unchanged by which
/// unit it was pressed on, while <em>show these in the graph</em> means <em>these rows</em> and
/// only makes sense where the rows are things the graph draws.</para>
///
/// <para>Measured on the crawled organization, the graph holds <c>step</c>, <c>job</c>,
/// <c>workflow</c>, <c>action</c>, <c>repo</c>, <c>composite_action</c>, <c>org</c>,
/// <c>reusable_workflow</c> and <c>docker_image</c>. A repository <b>is</b> a node — 290 of them —
/// and so is a job. An advisory is not.</para>
/// </summary>
public sealed class InventoryActionsTests : AbomTestContext
{
    /// The rule, stated where it can be read rather than spread across the markup.
    [Theory]
    [InlineData("dependencies")]
    [InlineData("workflows")]
    [InlineData("jobs")]
    [InlineData("repositories")]
    [InlineData("relationships")]
    [InlineData("coverage")]
    public void AUnitWhoseRowsAreDrawnOffersTheGraph(string unit)
    {
        Assert.True(Inventory.UnitDrawsInGraph(unit), $"{unit} rows are nodes the graph draws");
    }

    /// The one unit whose rows are not nodes. An advisory is a GHSA identifier: the graph can draw
    /// what *reaches* it, which is a different question from drawing the row.
    [Fact]
    public void TheAdvisoriesUnitDoesNotOfferTheGraph()
    {
        Assert.False(Inventory.UnitDrawsInGraph("advisories"));
    }

    /// Offered, but not silently: a control that vanishes leaves a reader who used it on the
    /// previous unit wondering what they did wrong.
    [Fact]
    public void TheGraphActionIsDisabledRatherThanHiddenOnAdvisories()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Answer("api/remediation", "[]");
        Go("/inventory?unit=advisories");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var action = page.Find("[data-abom-action='open-in-explorer']");
            Assert.Contains("disabled", action.OuterHtml);
        });
    }

    /// And it says why, on a span outside the disabled control: a disabled MudButton suppresses
    /// pointer events on its children, so a tooltip inside one never fires — and the tooltip a
    /// reader needs most is the one explaining the thing they cannot press.
    [Fact]
    public void ADisabledGraphActionSaysWhyItCannotApply()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Answer("api/remediation", "[]");
        Go("/inventory?unit=advisories");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var reason = page.Find("[data-abom-action-reason='open-in-explorer']");
            Assert.Contains("not", reason.GetAttribute("title") ?? "",
                System.StringComparison.OrdinalIgnoreCase);
        });
    }

    /// A bill of materials is about the scope and is the same document from every unit. Labelling
    /// it as though it described the rows on screen is the control ignoring the unit it was
    /// pressed on — so it says which it means instead of being removed.
    [Fact]
    public void TheScopeActionsSayTheyDescribeTheScopeRatherThanTheRows()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory?unit=jobs&rules=actions%2Funpinned-tag");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var group = page.Find("[data-abom-scope-actions]");
            Assert.Contains("scope", group.TextContent, System.StringComparison.OrdinalIgnoreCase);
        });
    }

    /// <summary>
    /// With nothing filtered and nothing named there is no scope, and *this scope:* over the whole
    /// corpus is a label claiming a narrowing that does not exist.
    /// </summary>
    [Fact]
    public void TheScopeActionsAreAbsentWhenThereIsNoScope()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Empty(page.FindAll("[data-abom-scope-actions]")));
    }

    /// And it comes back the moment something narrows, because then there is a scope to export.
    [Fact]
    public void TheScopeActionsReturnOnceSomethingNarrows()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory?rules=actions%2Funpinned-tag");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Single(page.FindAll("[data-abom-scope-actions]")));
    }

    /// <summary>
    /// The graph is a surface, not a picture the list drops you into — and a reader scanning a
    /// toolbar should be told where a control takes them rather than what it does to the canvas.
    /// </summary>
    [Fact]
    public void TheGraphActionNamesTheSurfaceItOpens()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory?rules=actions%2Funpinned-tag");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(
            "Explorer", page.Find("[data-abom-action='open-in-explorer']").TextContent));
    }

    /// <summary>
    /// A row carries its own actions, and they are icons: a column of words between Name and Type
    /// costs more width than the columns it sits between, and at organization scale width is the
    /// scarce thing. An icon with no accessible name is a button nobody can identify, so the
    /// tooltip is the label rather than a decoration.
    /// </summary>
    [Fact]
    public void ARowActionIsAnIconWithATooltipAndNoText()
    {
        Answer("api/actions", Row);
        Go("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var action = page.Find("[data-abom-row-action='open-in-explorer']");
            Assert.Empty(action.TextContent.Trim());
            Assert.False(string.IsNullOrWhiteSpace(action.GetAttribute("title")),
                "an icon-only control with no tooltip cannot be identified");
        });
    }

    /// The row action carries the row, not the page's scope. Handing over the scope would open the
    /// Explorer on everything the list was showing, which is the question the reader just left.
    [Fact]
    public void ARowActionOpensTheExplorerOnThatRow()
    {
        Answer("api/actions", Row);
        Go("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(
            Uri.EscapeDataString("actions/checkout"),
            page.Find("[data-abom-row-action='open-in-explorer']").GetAttribute("href") ?? ""));
    }

    /// <summary>
    /// A bill of materials narrows by grouping and nothing else, and the endpoint refuses every
    /// other term by name. The control has to refuse it first: dropping the terms it cannot apply
    /// and exporting anyway hands somebody the whole organization under the name of one part of
    /// it, which is measured and recorded — `units=3` once answered with all 25 roots.
    /// </summary>
    [Fact]
    public void AnExportIsRefusedWhenTheScopeHoldsTermsTheDocumentCannotApply()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory?scope=owned");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var reason = page.Find("[data-abom-action-reason='bom']");
            Assert.Contains("Scope", reason.GetAttribute("title") ?? "");
            Assert.Contains("disabled", page.Find("[data-abom-action='bom-cyclonedx']").OuterHtml);
        });
    }

    /// Narrowed only by what the document does apply, it exports.
    [Fact]
    public void AnExportIsOfferedWhenEveryTermIsOneTheDocumentApplies()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Answer("api/properties", """
            {"configured":"business_id","keys":["business_id"],
             "breakdown":{"name":"business_id","repositories":1,"withValue":1,"withoutValue":0,
               "values":[{"value":"3","repositories":1,"isDefault":false}],
               "hasDefault":false,"defaultValue":""}}
            """);
        Go("/inventory?properties=business_id%3A3");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.DoesNotContain(
            "disabled", page.Find("[data-abom-action='bom-cyclonedx']").OuterHtml));
    }

    private const string Row = """
        {"items":[{"nodeKey":"actions/checkout","kind":"action","origin":"external",
          "dependentRepos":50,"dependentSteps":null,"dependentJobs":70,"dependentWorkflows":40,
          "dependentGroups":6,"distinctRefs":3,
          "findings":2,"advisories":0,"analyzed":true,"refs":["v4"]}],"total":1,
         "groupProperty":"business_id"}
        """;

    /// <summary>
    /// What a reader weighs first leads: findings and advisories, then the versions in use, then
    /// what in the starting points depends on the action at each level they manage, steps up to
    /// groups. Total refs counted lines in repositories nobody here owns, and Repos reached is
    /// Depending Repositories.
    /// </summary>
    [Fact]
    public void TheActionsColumnsLeadWithRiskThenVersionsThenEveryLevelThatDependsOnIt()
    {
        Answer("api/actions", Row);
        Go("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-level='jobs']")));
        var headers = page.FindAll(".abom-actions-grid thead th")
            .Select(th => th.TextContent.Trim()).Where(h => h.Length > 0).ToList();
        Assert.Equal(new[]
        {
            "Name", "Type", "Origin", "Findings", "Advisories", "Distinct refs", "Depending Steps",
            "Depending Jobs", "Depending Workflows", "Depending Repositories", "Depending Groups", "Analyzed",
        }, headers);
        Assert.Equal("70", page.Find("[data-abom-level='jobs']").TextContent.Trim());
        Assert.Equal("6", page.Find("[data-abom-level='groups']").TextContent.Trim());
    }

    /// <summary>
    /// A level reach has not counted yet is not none, and a group count is only ever read against
    /// the property that divides the groups.
    /// </summary>
    [Fact]
    public void ALevelNotCountedYetSaysSoAndGroupsNameTheirProperty()
    {
        Answer("api/actions", Row);
        Go("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-level='steps']")));
        var steps = page.Find("[data-abom-level='steps']");
        Assert.Equal("\u2014", steps.TextContent.Trim());
        Assert.Contains("not counted", steps.GetAttribute("title"));
        var groups = page.FindAll(".abom-actions-grid thead th [data-abom-header-tip]")
            .Single(t => t.TextContent.Trim() == "Depending Groups");
        Assert.Contains("business_id", groups.GetAttribute("data-abom-header-tip"));
    }

    private void Go(string url) =>
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo(url);

    /// <summary>
    /// The list is what the starting points run or hold, so what it leaves out is counted where the
    /// reader can see it, and a row reach has not measured yet says so in every count, repositories
    /// and versions included.
    /// </summary>
    [Fact]
    public void WhatNoStartingPointRunsIsLeftOutAndCounted()
    {
        Answer("api/actions", """
            {"items":[{"nodeKey":"late/new","kind":"action","origin":"external",
              "dependentRepos":null,"dependentSteps":null,"dependentJobs":null,
              "dependentWorkflows":null,"dependentGroups":null,"distinctRefs":null,
              "findings":0,"advisories":0,"analyzed":false,"refs":["v1"]}],"total":1,
             "unlisted":402}
            """);
        Go("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-unlisted]")));
        var unlisted = string.Join(' ', page.Find("[data-abom-unlisted]").TextContent
            .Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries));
        Assert.Contains("402 more actions", unlisted);
        Assert.Contains("no starting point runs or holds", unlisted);
        Assert.Equal("\u2014", page.Find("[data-abom-level='repositories']").TextContent.Trim());
        Assert.Equal("\u2014", page.Find("[data-abom-level='refs']").TextContent.Trim());
        Assert.Contains("your starting points run or hold", page.Markup);
    }
}
