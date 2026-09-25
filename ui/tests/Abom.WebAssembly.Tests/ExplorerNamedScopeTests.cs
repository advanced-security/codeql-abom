using System.Linq;
using Abom.WebAssembly.Pages;
using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Explorer opens a named scope at any time and keeps the one on screen, like every page that
/// builds a scope. It offered named scopes only on the empty frame, and on the crawled organization
/// none of ten opened with the predicate it was kept with: every one gained the seven arrival kinds,
/// and four lost a term outright — a reference style twice, a rule and a relationship kind.
/// </summary>
public sealed class ExplorerNamedScopeTests : AbomTestContext
{
    private const string Views = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true,
          "refuses":{"graph":[]}},
         {"name":"unpinned","title":"Floating references","predicate":"ref=tag","builtin":true,
          "refuses":{"graph":[]}},
         {"name":"one-rule","predicate":"rules=actions%2Funpinned-tag","builtin":false,
          "refuses":{"graph":[]}},
         {"name":"needs","predicate":"edgeKinds=job-needs-job","builtin":false,
          "refuses":{"graph":[]}},
         {"name":"crossings","predicate":"boundary=cross-owner","builtin":false,
          "refuses":{"graph":[]}},
         {"name":"upstream-rules","predicate":"source=upstream","builtin":false,
          "refuses":{"graph":["source"]}},
         {"name":"drawn","predicate":"scope=owned","builtin":false,
          "presentation":"view=priority&groupBy=repo&hops=2&use3d=true&selection=actions%2Fcheckout",
          "refuses":{"graph":[]}}]
        """;

    public ExplorerNamedScopeTests()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        Answer("api/views", Views);
        Answer("api/summary", "{}");
        Answer("api/properties", """
            {"configured":"","keys":[],
             "breakdown":{"name":"","repositories":0,"withValue":0,"withoutValue":0,
              "values":[],"hasDefault":false,"defaultValue":""}}
            """);
    }

    private static readonly HashSet<string> PresentationKeys =
    [
        "view", "direction", "from", "status", "hops", "groupBy", "use3d", "collapsed",
        "stableLayout", "selection",
    ];

    private string Address =>
        new Uri(Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri).Query;

    /// The predicate half of the address, as a stored view would hold it.
    private string Predicate => string.Join("&", Address.TrimStart('?')
        .Split('&', StringSplitOptions.RemoveEmptyEntries)
        .Where(p => !PresentationKeys.Contains(p.Split('=')[0]))
        .OrderBy(p => p, StringComparer.Ordinal));

    private static string Chip(string name) => $"[data-abom-views] [data-abom-view='{name}']";

    private T Filter<T>(string field)
    {
        var spec = JSInterop.Invocations["applyFilters"].Last().Arguments[1]!;
        return (T)spec.GetType().GetProperty(field)!.GetValue(spec)!;
    }

    /// Named scopes stay on offer once something is drawn, so a second one can be opened without
    /// clearing the first by hand.
    [Fact]
    public void TheNamedScopesStayOnOfferOnceSomethingIsDrawn()
    {
        StubGraphModule();
        ArriveWithAScope("scope=exposure&flags=findings");

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.NotNull(page.Find(Chip("owned"))));
        Assert.Empty(page.FindAll("[data-abom-no-scope]"));
    }

    /// <summary>
    /// Opening a named scope asks its question whole. A term the scope does not name is this page's
    /// arrival choice for it, never whatever the scope on screen a moment ago had set.
    /// </summary>
    [Fact]
    public void OpeningANamedScopeOverAnotherAsksItsQuestionWhole()
    {
        StubGraphModule();
        ArriveWithAScope("rules=abom%2Fx&ref=sha&q=pay&scope=exposure&flags=findings");

        var page = Render<Explorer>();
        page.WaitForElement(Chip("owned")).Click();

        page.WaitForAssertion(() => Assert.Equal("scope=owned", Predicate));
        Assert.Contains("scope=owned",
            (string)JSInterop.Invocations["loadFull"].Last().Arguments[1]!);
        Assert.Equal("any", Filter<string>("refStyle"));
        Assert.Empty(Filter<string[]>("rules"));
    }

    /// <summary>
    /// A scope kept anywhere opens here with the predicate it was kept with, compared as the text
    /// the server stored.
    /// </summary>
    [Theory]
    [InlineData("unpinned", "ref=tag")]
    [InlineData("one-rule", "rules=actions%2Funpinned-tag")]
    [InlineData("needs", "edgeKinds=job-needs-job")]
    [InlineData("crossings", "boundary=cross-owner")]
    [InlineData("owned", "scope=owned")]
    public void ANamedScopeOpensWithThePredicateItWasKeptWith(string name, string predicate)
    {
        StubGraphModule();

        var page = Render<Explorer>();
        page.WaitForElement(Chip(name)).Click();

        page.WaitForAssertion(() => Assert.Equal(predicate, Predicate));
    }

    /// The graph refuses a source, so the Explorer offers no scope carrying one.
    [Fact]
    public void AScopeTheGraphRefusesIsNotOpenedHere()
    {
        StubGraphModule();

        var page = Render<Explorer>();

        page.WaitForAssertion(() => Assert.Contains("mud-disabled",
            page.Find(Chip("upstream-rules")).ClassName ?? ""));
    }

    /// <summary>
    /// Keeping here sends the picture beside the question. The Explorer is the only page that
    /// draws, and the only Keep on offer used to be one that sent none.
    /// </summary>
    [Fact]
    public void KeepingInTheExplorerSendsThePictureBesideTheQuestion()
    {
        StubGraphModule();
        ArriveWithAScope("scope=owned&view=priority&groupBy=repo");

        var page = Render<Explorer>();
        page.WaitForElement("[data-abom-named-scopes] [data-abom-scope-name]").Input("mine");
        page.Find("[data-abom-named-scopes] [data-abom-save-scope]").Click();

        page.WaitForAssertion(() =>
        {
            var put = Sent.Last(r => r.Url.Contains("api/views/"));
            Assert.Equal("PUT", put.Method);
            Assert.Contains("scope=owned", put.Body);
            Assert.DoesNotContain("kinds=", put.Body);
            Assert.Contains("view=priority", put.Body);
            Assert.Contains("groupBy=repo", put.Body);
        });
    }

    /// <summary>
    /// A scope kept here draws back the same picture here, down to the node in focus: naming a
    /// node narrows the drawing to its neighborhood, and a picture that lost it is a different one.
    /// </summary>
    [Fact]
    public void AScopeKeptOnTheExplorerDrawsBackTheSamePicture()
    {
        StubGraphModule();
        ArriveWithAScope("scope=exposure&flags=findings");

        var page = Render<Explorer>();
        page.WaitForElement(Chip("drawn")).Click();

        page.WaitForAssertion(() =>
        {
            var drawn = page.Instance.CurrentPresentation;
            Assert.Equal("priority", drawn.View);
            Assert.Equal("repo", drawn.GroupBy);
            Assert.Equal(2, drawn.Hops);
            Assert.True(drawn.Use3d);
            Assert.Equal("actions/checkout", drawn.Selection);
        });
        Assert.Equal("scope=owned", Predicate);
        Assert.Contains("selection=actions%2Fcheckout", Address);
    }

    /// <summary>
    /// The arrival kinds are what the payload holds without a focus, so spelled out they narrow
    /// nothing here — and on any other page the scope is opened on, they list kinds that page never
    /// shows. They still reach the browser, or an unnamed list would draw a blank canvas.
    /// </summary>
    [Fact]
    public void TheArrivalKindsReachTheBrowserAndNotTheScope()
    {
        StubGraphModule();
        ArriveWithAScope("scope=owned");

        var page = Render<Explorer>();
        page.Instance.SyncAddress();

        page.WaitForAssertion(() => Assert.DoesNotContain("kinds=", Address));
        Assert.Equal(7, Filter<string[]>("kinds").Length);
    }

    /// <summary>
    /// Changing a filter keeps every relationship class and the arrival kinds. The picker's change
    /// read both lists back out of a scope that deliberately writes neither when it is the default,
    /// and on the crawled organization removing one chip hid all 125 relationships between the 88
    /// nodes left on screen.
    /// </summary>
    [Fact]
    public void ChangingAFilterKeepsEveryRelationshipClassAndTheArrivalKinds()
    {
        StubGraphModule();
        ArriveWithAScope("q=pay&scope=owned");

        var page = Render<Explorer>();
        page.WaitForElement("[data-abom-filter-chip='q'] [data-abom-chip-remove]").Click();

        page.WaitForAssertion(() => Assert.DoesNotContain("q=pay", Address));
        Assert.Equal(3, Filter<string[]>("edgeClasses").Length);
        Assert.Equal(7, Filter<string[]>("kinds").Length);
    }

    private void StubGraphModule()
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(new Explorer.Counts(0, 0, 0, 0));
        foreach (var name in new[]
                 {
                     "setPalette", "setAnimate", "setGroupBy", "setCollapsed", "setView",
                     "setGeneration", "setSelection", "resize", "rebuildNodes", "setMode",
                     "setPriorities",
                 })
        {
            module.SetupVoid(name, _ => true).SetVoidResult();
        }
    }
}
