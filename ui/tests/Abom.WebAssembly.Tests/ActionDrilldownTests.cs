using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// From a row in the Actions unit to one version of one action: the row opens in place onto the
/// versions in use, a reference opens the details page already narrowed to it, and the details
/// page answers who uses it under the same scope the list was drawn with. These pin what each step
/// may claim, and what it must carry forward.
/// </summary>
public sealed class ActionDrilldownTests : AbomTestContext
{
    private const string Checkout = "actions/checkout";
    private const string V4Commit = "11bd71901bbe5b1630ceea73d27597364c9af683";

    private const string Actions = """
        {"items":[
          {"nodeKey":"actions/checkout","kind":"action","owner":"actions","repo":"checkout","origin":"",
           "dependentRepos":19,"dependentSteps":6468,"distinctRefs":53,"findings":0,
           "severity":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"advisories":0,
           "analyzed":true,"refs":["v4"]},
          {"nodeKey":"actions/cache","kind":"action","owner":"actions","repo":"cache","origin":"",
           "dependentRepos":5,"dependentSteps":152,"distinctRefs":11,"findings":2,
           "severity":{"critical":0,"high":2,"medium":0,"low":0,"unknown":0},"advisories":0,
           "analyzed":true,"refs":["v3"]}],
         "total":2}
        """;

    private const string Versions = $$$"""
        {"key":"actions/checkout","total":4,
         "items":[
           {"reference":"v4","commit":"{{{V4Commit}}}","queue":"analyzed",
            "lockedTo":[{"commit":"2222222222222222222222222222222222222222","repositories":1}],
            "direct":12,"transitive":3,"lines":1706,
            "findings":{"count":2,"severity":{"critical":1,"high":1,"medium":0,"low":0,"unknown":0}},
            "lineFindings":{"count":3,"severity":{"critical":0,"high":1,"medium":2,"low":0,"unknown":0},
                            "commits":2,"analyzed":2,"unknown":0}
           },
           {"reference":"v1","commit":"","queue":"","lockedTo":[],"direct":1,"transitive":0,
            "lines":2,"findings":null,"lineFindings":null},
           {"reference":"{{{V4Commit}}}","commit":"{{{V4Commit}}}","queue":"analyzed","lockedTo":[],
            "direct":1,"transitive":0,"lines":1,"findings":null,
            "lineFindings":{"count":0,"severity":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},
                            "commits":1,"analyzed":1,"unknown":0}},
           {"reference":"release/2024-hotfix-branch","commit":"","queue":"","lockedTo":[],
            "direct":1,"transitive":0,"lines":1,"findings":null,
            "lineFindings":{"count":1,"severity":{"critical":0,"high":1,"medium":0,"low":0,"unknown":0},
                            "commits":3,"analyzed":1,"unknown":2}}],
         "advisories":[
           {"ghsaId":"GHSA-checkout","severity":"high","summary":"A token leaks","url":"https://github.com/advisories/GHSA-checkout",
            "packageName":"actions/checkout","ranges":[{"vulnerableRange":">= 4.0.0, < 4.1.3","firstPatched":"4.1.3"}],
            "nodes":[]}]}
        """;

    private const string Detail = """
        {"key":"actions/checkout","kind":"action","repository":"","origin":"","path":"",
         "isExternal":true,"analyzed":true,"refsInUse":72,"refsAnalyzed":29,"dependentRepos":19,
         "rules":[],"uses":[]}
        """;

    private static string Uses(string change = "") => $$"""
        {"key":"actions/checkout","total":2,"hiddenUpstream":40,"unanalyzed":7,{{change}}
         "items":[
           {"repository":"octo/app","origin":"seed","reference":"v4","commit":"{{V4Commit}}",
            "locked":false,"hops":1,"through":"","path":["octo/app/.github/workflows/ci.yml","actions/checkout"],
            "conditions":[],"workflows":["octo/app/.github/workflows/ci.yml"],"lines":2},
           {"repository":"octo/tool","origin":"seed","reference":"v4","commit":"{{V4Commit}}",
            "locked":false,"hops":2,"through":"docker/build-push-action",
            "path":["octo/tool/.github/workflows/ci.yml","docker/build-push-action","actions/checkout"],
            "conditions":[{"in":"docker/build-push-action","condition":"runner.os == 'Linux'"}],
            "workflows":["octo/tool/.github/workflows/ci.yml"],"lines":0}]}
        """;

    private void Go(string url) =>
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo(url);

    private string Address => Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri;

    // More references than the panel holds at once, each named directly by one more starting point
    // than the next, so the order is the server's and the last one is on the last page.
    private static string ManyVersions(int count) =>
        $$"""{"key":"actions/checkout","total":{{count}},"advisories":[],"items":[""" +
        string.Join(",", Enumerable.Range(1, count).Select(i =>
            $$"""{"reference":"v{{i}}","commit":"{{new string('a', 40)}}","queue":"analyzed","lockedTo":[],"direct":{{count - i + 1}},"transitive":0,"lines":1,"findings":null}""")) +
        "]}";

    private IRenderedComponent<Pages.Inventory> Inventory(string address = "/inventory", string versions = Versions)
    {
        Answer("/-/versions", versions);
        Answer("api/actions", Actions);
        Answer("api/views", "[]");
        Go(address);
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-action-expand]")));
        return page;
    }

    private static AngleSharp.Dom.IElement Row(IRenderedComponent<Pages.Inventory> page, string key) =>
        page.Find($"[data-abom-action-expand='{key}']").Closest("tr")!;

    // The middle ground the drill-down exists for: a row opens where it is, onto the versions in
    // use, without leaving the list.
    [Fact]
    public void ClickingARowOpensItsVersionsInPlace()
    {
        var page = Inventory();

        // A reader clicks somewhere in the row — a cell's text — never the row element itself.
        Row(page, Checkout).QuerySelectorAll("td").First(td => td.TextContent.Contains("6,468")).Click();

        page.WaitForAssertion(() => Assert.NotNull(page.Find($"[data-abom-action-panel='{Checkout}']")));
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-ref='v4']")));
        Assert.Equal("true", page.Find($"[data-abom-action-expand='{Checkout}']").GetAttribute("aria-expanded"));
        Assert.Contains("abom-row-open", Row(page, Checkout).ClassName);
        // The panel lists versions; who names each one is the details page's to open on request.
        // A grid with a row body and no control for it draws that body under every row.
        Assert.Empty(page.FindAll("[data-abom-ref-users-of]"));
        Assert.DoesNotContain(Sent, r => r.Url.Contains("/-/users"));
    }

    // actions/checkout is named at dozens of references. The panel holds one page of them so the
    // actions below the open one stay on screen, and its pager reaches the rest in place.
    [Fact]
    public void ThePanelHoldsOnePageOfVersionsAndPagesThroughTheRest()
    {
        var count = Shared.ActionVersions.PanelRows + 3;
        var page = Inventory(versions: ManyVersions(count));
        var panel = $"[data-abom-action-panel='{Checkout}']";

        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll($"{panel} [data-abom-version-ref='v1']")));
        Assert.Equal(Shared.ActionVersions.PanelRows, page.FindAll($"{panel} [data-abom-version-ref]").Count);

        page.Find($"{panel} button[aria-label='Next page']").Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll($"{panel} [data-abom-version-ref='v{count}']")));
        Assert.Equal(3, page.FindAll($"{panel} [data-abom-version-ref]").Count);
        Assert.NotNull(page.Find(panel));
    }

    // The panel sits inside a table of actions, where every extra column is one more a reader can
    // match to the wrong header. Locks and line counts stay on the details page.
    [Fact]
    public void ThePanelLeavesLocksAndLinesToTheDetailsPage()
    {
        var page = Inventory();
        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-ref='v4']")));

        var headers = page.FindAll($"[data-abom-action-panel='{Checkout}'] th")
            .Select(th => th.TextContent.Trim()).ToList();
        Assert.Contains("Reference", headers);
        Assert.Contains("Resolves to", headers);
        Assert.Contains("Findings", headers);
        foreach (var elsewhere in new[] { "Locked to", "uses: lines", "Findings on lines", "Analysis" })
        {
            Assert.DoesNotContain(elsewhere, headers);
        }
        Assert.Empty(page.FindAll("[data-abom-version-line-findings]"));
        // What the analysis column said survives where it matters: on a version nothing analyzed.
        Assert.Contains("never queued", page.Find("[data-abom-version-findings='v1']").GetAttribute("title"));
    }

    // One at a time: a second open row would push the first off screen, and two panels side by
    // side read as one table.
    [Fact]
    public void OpeningAnotherRowClosesTheFirst()
    {
        var page = Inventory();

        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotNull(page.Find($"[data-abom-action-panel='{Checkout}']")));
        Row(page, "actions/cache").Click();

        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-action-panel='actions/cache']")));
        Assert.Empty(page.FindAll($"[data-abom-action-panel='{Checkout}']"));
        Assert.Equal("false", page.Find($"[data-abom-action-expand='{Checkout}']").GetAttribute("aria-expanded"));
    }

    // Clicking the open row again, or pressing Escape inside its panel, puts it away.
    [Fact]
    public void TheOpenRowClosesOnASecondClickOrOnEscape()
    {
        var page = Inventory();

        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotNull(page.Find($"[data-abom-action-panel='{Checkout}']")));
        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.Empty(page.FindAll("[data-abom-action-panel]")));

        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotNull(page.Find($"[data-abom-action-panel='{Checkout}']")));
        page.Find($"[data-abom-action-panel='{Checkout}']").KeyDown(new Microsoft.AspNetCore.Components.Web.KeyboardEventArgs { Key = "Escape" });
        page.WaitForAssertion(() => Assert.Empty(page.FindAll("[data-abom-action-panel]")));
    }

    // The name and the panel's Details link both go to the details page under the scope the list
    // was drawn with, and a reference goes there narrowed to that reference.
    [Fact]
    public void TheDetailsAndAReferenceCarryTheScopeForward()
    {
        var page = Inventory("/inventory?properties=team%3Acore&ref=tag");
        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-ref='v4']")));

        Assert.Equal("/node/actions%2Fcheckout?properties=team%3Acore&ref=tag",
            page.Find($"[data-abom-action-details='{Checkout}']").GetAttribute("href"));
        Assert.Equal("/node/actions%2Fcheckout?at=v4&properties=team%3Acore&ref=tag",
            page.Find("[data-abom-version-ref='v4']").GetAttribute("href"));
        // The panel asks only what the uses surface applies, so the endpoint is never refused.
        Assert.Contains(Sent, r => r.Url.Contains("/-/versions") && r.Url.Contains("ref=tag")
                                   && r.Url.Contains("properties=team"));
    }

    // A version's findings say what was reported at its commit, and one nothing analyzed says so
    // rather than reading as a clean zero.
    [Fact]
    public void AVersionSaysWhatWasFoundAtItsCommitAndWhatIsNotKnown()
    {
        var page = Inventory();
        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-ref='v4']")));

        Assert.Contains("2", page.Find("[data-abom-version-findings='v4']").TextContent);
        Assert.Contains("not analyzed", page.Find("[data-abom-version-findings='v1']").TextContent);
        Assert.Contains("not resolved", page.Find("[data-abom-version-ref='v1']").Closest("tr")!.TextContent);
    }

    // An action its own repository runs only by local path has no version anyone wrote. An empty
    // table would read as nothing using it; it says why there is no row, and a scope that matched
    // none of the references in use says that instead.
    [Fact]
    public void AnEmptyVersionsTableSaysWhyItIsEmpty()
    {
        const string none = """{"key":"actions/checkout","total":0,"advisories":[],"items":[]}""";
        var page = Inventory(versions: none);
        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.Contains("local path", page.Find("[data-abom-versions-none]").TextContent));

        const string others = """{"key":"actions/checkout","total":0,"unlisted":25,"advisories":[],"items":[]}""";
        var elsewhere = Inventory(versions: others);
        Row(elsewhere, Checkout).Click();
        elsewhere.WaitForAssertion(() =>
            Assert.Contains("No starting point", elsewhere.Find("[data-abom-versions-none]").TextContent));

        const string unmatched = """{"key":"actions/checkout","total":3,"advisories":[],"items":[]}""";
        var scoped = Inventory(versions: unmatched);
        Row(scoped, Checkout).Click();
        scoped.WaitForAssertion(() => Assert.Contains("None of the 3", scoped.Find("[data-abom-versions-none]").TextContent));
    }

    // A header names a column; what its values are is a line under the pointer. A header left without
    // one is a number a reader has to guess the meaning of.
    [Fact]
    public void EveryColumnHeaderSaysWhatItsValuesAre()
    {
        var list = Inventory();
        Row(list, Checkout).Click();
        list.WaitForAssertion(() => Assert.NotEmpty(list.FindAll("[data-abom-version-ref='v4']")));
        Assert.Empty(Untipped(list.FindAll("th")));

        var node = Node();
        node.WaitForAssertion(() => Assert.NotEmpty(node.FindAll("[data-abom-version-ref='v4']")));
        Assert.Empty(Untipped(node.FindAll("th")));
    }

    // A tip is read by a person, so it is a sentence: capitalized, ended, and more than a label.
    private static List<string> Untipped(IEnumerable<AngleSharp.Dom.IElement> headers) => headers
        .Where(th => th.TextContent.Trim().Length > 0)
        .Where(th => th.QuerySelector("[data-abom-header-tip]")?.GetAttribute("data-abom-header-tip") is not { } tip
                     || !char.IsUpper(tip[0]) || !tip.EndsWith('.') || tip.Split(' ').Length < 6)
        .Select(th => th.TextContent.Trim()).ToList();

    // No column is one rule's. Which rules matter is the reader's question to scope, and a column
    // named after one put the others nowhere.
    [Fact]
    public void NoVersionsColumnIsNamedAfterARule()
    {
        var node = Node();
        node.WaitForAssertion(() => Assert.NotEmpty(node.FindAll("[data-abom-version-ref='v4']")));

        var headers = node.FindAll("[data-abom-versions-table] thead th").Select(th => th.TextContent.Trim());
        Assert.DoesNotContain(headers, h => h.Contains('/'));
    }

    // A reader manages what their starting points run. How many repositories they do not own use a
    // version is nothing to act on, so no column counts them; the versions only those repositories
    // use are left out, and the summary says how many.
    [Fact]
    public void VersionsOnlyOthersUseAreLeftOutAndSaidAndNeverCounted()
    {
        var versions = Versions.Replace("\"total\":4,", "\"total\":4,\"unlisted\":21,");
        var list = Inventory(versions: versions);
        Row(list, Checkout).Click();
        list.WaitForAssertion(() => Assert.NotEmpty(list.FindAll("[data-abom-version-ref='v4']")));
        Assert.Contains("21 more", list.Find("[data-abom-versions-summary]").TextContent);

        var node = Node(versions: versions);
        node.WaitForAssertion(() => Assert.NotEmpty(node.FindAll("[data-abom-version-ref='v4']")));
        foreach (var (headers, summary) in new[]
                 {
                     (list.FindAll("[data-abom-versions-table] thead th"), list.Find("[data-abom-versions-summary]")),
                     (node.FindAll("[data-abom-versions-table] thead th"), node.Find("[data-abom-versions-summary]")),
                 })
        {
            Assert.DoesNotContain("Upstream", headers.Select(th => th.TextContent.Trim()));
            Assert.DoesNotContain("upstream", summary.TextContent);
        }
    }

    // Every column of the versions table sorts, findings above all: a template column given only a
    // sort key drew a header that could not be clicked, so the riskiest version could not be put
    // first.
    [Fact]
    public void EveryColumnOfTheVersionsTableSorts()
    {
        var page = Node();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-ref='v4']")));

        var unsortable = page.FindAll("[data-abom-versions-table] thead th .column-header")
            .Where(h => h.QuerySelector(".sortable-column-header") is null && h.TextContent.Trim().Length > 0)
            .Select(h => h.TextContent.Trim()).ToList();
        Assert.Empty(unsortable);
    }

    // A reference that is itself a commit is written the way commits are, whole only in its title,
    // so forty characters do not set the width of the column. A branch name is never cut.
    [Fact]
    public void ACommitReferenceIsShortenedAndABranchNameIsNot()
    {
        var page = Inventory();
        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-ref='v4']")));

        var pinned = page.Find($"[data-abom-version-ref='{V4Commit}']");
        Assert.Equal(V4Commit[..12], pinned.TextContent.Trim());
        Assert.Contains(V4Commit, pinned.GetAttribute("title"));
        Assert.Equal("release/2024-hotfix-branch",
            page.Find("[data-abom-version-ref='release/2024-hotfix-branch']").TextContent.Trim());
    }

    // The advisory travels verbatim beside the references, with its range and its fix, and is never
    // turned into a count of affected versions.
    [Fact]
    public void TheAdvisoriesNamingTheActionAreListedVerbatimBesideTheVersions()
    {
        var page = Inventory();
        Row(page, Checkout).Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-advisory]")));

        var advisory = page.Find("[data-abom-version-advisory='GHSA-checkout']").TextContent;
        Assert.Contains(">= 4.0.0, < 4.1.3", advisory);
        Assert.Contains("4.1.3", advisory);
        Assert.DoesNotContain("vulnerable", page.Find("[data-abom-version-ref='v4']").Closest("tr")!.TextContent,
            StringComparison.OrdinalIgnoreCase);
    }

    private IRenderedComponent<Pages.NodePage> Node(string query = "", string change = "", string? uses = null,
        string versions = Versions)
    {
        Answer("/-/versions", versions);
        Answer("/-/uses", uses ?? Uses(change));
        Answer("api/node/", Detail);
        Go($"/node/{Checkout}{query}");
        var page = Render<Pages.NodePage>(p => p.Add(c => c.Key, Checkout));
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-use-repository]")));
        return page;
    }

    // What any rule reported on the lines naming a version is counted at the commit each line was
    // analyzed. Lines no findings run reached are not analyzed rather than clean, a version no
    // starting point names on a line of its own has nothing there to count, and commits a findings
    // run did not reach or whose declaration was never recorded are said beside the count.
    [Fact]
    public void TheFindingsOnAVersionsLinesSayWhatWasReportedThereAndWhatIsNotKnown()
    {
        var page = Node();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-line-findings='v4']")));

        Assert.Equal("3", page.Find("[data-abom-version-line-findings='v4'] .abom-num").TextContent.Trim());
        Assert.Equal("0", page.Find($"[data-abom-version-line-findings='{V4Commit}'] .abom-num").TextContent.Trim());
        Assert.Contains("not analyzed", page.Find("[data-abom-version-line-findings='v1']").TextContent);
        var partial = page.Find("[data-abom-version-line-findings='release/2024-hotfix-branch']").TextContent;
        Assert.Contains("1 of 3 commits", partial);
        Assert.Contains("2 unknown", partial);
        Assert.DoesNotContain("commits", page.Find("[data-abom-version-line-findings='v4']").TextContent);

        var transitive = Node(versions: """
            {"key":"actions/checkout","total":1,"advisories":[],"items":[
              {"reference":"v5","commit":"","queue":"","lockedTo":[],"direct":0,"transitive":2,
               "lines":0,"findings":null,"lineFindings":null}]}
            """);
        transitive.WaitForAssertion(() => Assert.NotEmpty(transitive.FindAll("[data-abom-version-line-findings='v5']")));
        var cell = transitive.Find("[data-abom-version-line-findings='v5']").TextContent;
        Assert.Contains("no lines", cell);
        Assert.DoesNotContain("not analyzed", cell);
    }

    // A use writing no reference is a local path, which runs at its caller's own commit. It was drawn
    // as the default branch, which no workflow wrote.
    [Fact]
    public void AUseWritingNoReferenceSaysItIsALocalPath()
    {
        var page = Node(uses: $$"""
            {"key":"actions/checkout","total":1,"hiddenUpstream":0,"unanalyzed":0,
             "items":[{"repository":"actions/checkout","origin":"seed","reference":"","commit":"{{V4Commit}}",
               "locked":false,"hops":1,"through":"","path":["actions/checkout/.github/workflows/test.yml","actions/checkout"],
               "conditions":[],"workflows":["actions/checkout/.github/workflows/test.yml"],"lines":1}]}
            """);

        var reference = page.Find("[data-abom-use-repository='actions/checkout']").Closest("tr")!
            .QuerySelector("[data-abom-use-reference]")!.TextContent;
        Assert.Equal("local path", reference.Trim());
    }

    // Big numbers in boxes linking nowhere repeated what the sections below carry. Each figure now
    // lives in the sentence or column it qualifies.
    [Fact]
    public void TheDetailsPageCarriesNoCountTiles()
    {
        var page = Node();

        foreach (var caption in new[] { "repositories reach it", "lines point here", "distinct references", "references analyzed" })
        {
            Assert.DoesNotContain(caption, page.Markup);
        }
        Assert.Empty(page.FindAll("h6.abom-num, .mud-typography-h6.abom-num"));
        Assert.NotNull(page.Find("[data-abom-versions-summary]"));
    }

    // The address is the scope: a reference in it narrows both tables, and a term the page does
    // not apply stays in the address, named, rather than reaching an endpoint that would refuse
    // the whole question.
    [Fact]
    public void TheAddressNarrowsBothTablesAndUnappliedTermsStayOutOfTheirQuestions()
    {
        var page = Node("?at=v4&kinds=action");

        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("/-/uses")));
        foreach (var route in new[] { "/-/uses", "/-/versions" })
        {
            var sent = Sent.Last(r => r.Url.Contains(route)).Url;
            Assert.Contains("at=v4", sent);
            Assert.DoesNotContain("kinds", sent);
        }
        Assert.NotEmpty(page.FindAll("[data-abom-chip-unapplied]"));
    }

    // One row per repository, reference and commit, and the relationship says which is the
    // repository's own to change.
    [Fact]
    public void EachUseNamesItsReferenceCommitAndRelationship()
    {
        var page = Node();

        var app = page.Find("[data-abom-use-repository='octo/app']").Closest("tr")!;
        Assert.Contains("v4", app.QuerySelector("[data-abom-use-reference]")!.TextContent);
        Assert.Contains(V4Commit[..12], app.QuerySelector("[data-abom-use-commit]")!.TextContent);
        Assert.Contains("direct", app.QuerySelector("[data-abom-use-relationship]")!.TextContent);
        Assert.Contains("the reference", app.QuerySelector("[data-abom-use-chosen]")!.TextContent);
        var tool = page.Find("[data-abom-use-repository='octo/tool']").Closest("tr")!;
        Assert.Contains("transitive", tool.QuerySelector("[data-abom-use-relationship]")!.TextContent);
        Assert.Contains("docker/build-push-action", tool.TextContent);
    }

    // The route and its guard open under the row, verbatim, and an unguarded route says so.
    [Fact]
    public void AUsesRouteAndItsConditionsOpenUnderTheRow()
    {
        var page = Node();

        foreach (var repository in new[] { "octo/app", "octo/tool" })
        {
            page.Find($"[data-abom-use-repository='{repository}']").Closest("tr")!
                .QuerySelector("[data-abom-use-detail]")!.Click();
        }

        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-use-route='octo/tool']")));
        Assert.Contains("runner.os == 'Linux'", page.Find("[data-abom-use-route='octo/tool']").TextContent);
        Assert.Contains("unconditional", page.Find("[data-abom-use-route='octo/app']").TextContent);
    }

    // What the default left out is counted, with the way to see it, and what has not been analyzed
    // is named rather than silently absent.
    [Fact]
    public void TheUsesTableSaysWhatItLeftOutAndWhatIsNotKnown()
    {
        var page = Node();

        var said = page.Find("[data-abom-uses-said]").TextContent;
        Assert.Contains("40 upstream repositories", said);
        Assert.Contains("7 queued dependencies", said);
        Assert.Contains("origins=seed", page.Find("[data-abom-uses-hidden-upstream] a").GetAttribute("href") ?? "");
    }

    // With one tag asked for, the page says what moving it would do, and never adds the two kinds
    // of repository together.
    [Fact]
    public void OneTagAskedForSaysWhoMovesWhoFollowsAndWhoALockKeeps()
    {
        var page = Node("?at=v4", $$"""
            "change":{"reference":"v4","fixed":false,"namesNow":"{{V4Commit}}",
                      "direct":["octo/app"],"transitive":["octo/tool"],"kept":["octo/locked"]},
            """);

        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-tag-change]")));
        Assert.Contains(V4Commit[..12], page.Find("[data-abom-tag-names-now]").TextContent);
        Assert.Contains("octo/app", page.Find("[data-abom-tag-change-group='direct']").TextContent);
        Assert.Contains("octo/tool", page.Find("[data-abom-tag-change-group='transitive']").TextContent);
        Assert.Contains("octo/locked", page.Find("[data-abom-tag-change-group='kept']").TextContent);
        Assert.Empty(page.FindAll("[data-abom-tag-change-hint]"));
    }

    // A commit does not move, and with no single reference asked for the page says how to ask.
    [Fact]
    public void ACommitDoesNotMoveAndNoReferenceAskedForSaysHowToAsk()
    {
        var page = Node("?at=" + V4Commit, $$"""
            "change":{"reference":"{{V4Commit}}","fixed":true,"namesNow":"","direct":[],"transitive":[],"kept":[]},
            """);
        page.WaitForAssertion(() => Assert.Contains("does not move", page.Find("[data-abom-tag-change]").TextContent));
        Assert.Empty(page.FindAll("[data-abom-tag-change-group]"));

        var unasked = Node();
        Assert.Empty(unasked.FindAll("[data-abom-tag-change]"));
        Assert.NotNull(unasked.Find("[data-abom-tag-change-hint]"));
    }

    // A reference chosen on the details page narrows it in place, keeping the rest of the scope.
    [Fact]
    public void AReferenceOnTheDetailsPageNarrowsThePageItself()
    {
        var page = Node("?properties=team%3Acore");
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-version-ref='v4']")));

        Assert.Equal($"/node/{Checkout}?at=v4&properties=team%3Acore",
            page.Find("[data-abom-version-ref='v4']").GetAttribute("href"));
        Assert.Equal("/inventory?properties=team%3Acore",
            page.Find("[data-abom-node-inventory]").GetAttribute("href"));
    }
}
