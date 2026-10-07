using System.Linq;
using System.Net;
using Abom.WebAssembly.Models;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// One control for named scopes, on every page that builds a scope. Whether a page can open one is
/// the server's answer for that page: the Inventory's chips used to read the node listing's refusal
/// whatever unit was on screen, and on the crawled organization that offered 26 of 60 chips that
/// the unit then refused on click.
/// </summary>
public sealed class NamedScopesTests : AbomTestContext
{
    private const string Views = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true,
          "description":"Only what the repositories you declared hold.",
          "refuses":{"inventory":[],"repositories":[],"advisories":["scope"],"graph":[],"run":[]}},
         {"name":"unpinned","title":"Floating references","predicate":"ref=tag","builtin":true,
          "description":"What a workflow references by a tag or branch rather than a commit SHA.",
          "refuses":{"inventory":[],"workflows":["ref"],"repositories":["ref"],
                     "relationships":[],"coverage":["ref"],"graph":[],"run":["ref"]}},
         {"name":"upstream-rules","predicate":"source=upstream","builtin":false,
          "description":"Keeps Source: upstream.",
          "refuses":{"inventory":[],"graph":["source"],"run":[]}},
         {"name":"unreadable","predicate":"ref=pinned","builtin":false},
         {"name":"coverage-gaps","title":"Coverage gaps","predicate":"flags=unanalyzed&ref=tag","builtin":true,
          "description":"Referenced by a tag and never analyzed.",
          "refuses":{"inventory":[],"workflows":["ref","flags=unanalyzed"],"graph":[],"run":["ref"]}}]
        """;

    private const string Terms = """
        [{"name":"scope","label":"Scope","help":"h","arity":"scalar","surfaces":["inventory"]},
         {"name":"ref","label":"Reference style","help":"h","arity":"scalar","surfaces":["inventory"]},
         {"name":"source","label":"Source","help":"h","arity":"list","surfaces":["inventory"]},
         {"name":"flags","label":"Has","help":"h","arity":"list","surfaces":["inventory","workflows"]}]
        """;

    public NamedScopesTests()
    {
        Answer("api/views", Views);
        Answer("api/filters", Terms);
    }

    private IRenderedComponent<Shared.NamedScopes> Control(
        string surface, Func<string>? presentation = null, Action<NamedView>? chosen = null) =>
        Render<Shared.NamedScopes>(p =>
        {
            p.Add(x => x.Surface, surface);
            if (presentation is not null) p.Add(x => x.Presentation, presentation);
            p.Add(x => x.Chosen, (NamedView v) => chosen?.Invoke(v));
        });

    private static bool Disabled(IRenderedComponent<Shared.NamedScopes> control, string name) =>
        control.Find($"[data-abom-view='{name}']").ClassList.Contains("mud-disabled");

    private static string Reason(IRenderedComponent<Shared.NamedScopes> control, string name) =>
        control.Find($"[data-abom-view-reason='{name}']").GetAttribute("title") ?? "";

    /// <summary>
    /// A scope this page would refuse is disabled here with the terms it cannot apply named, and
    /// with where it does open — read from the server's answer for every page, not assumed.
    /// </summary>
    [Fact]
    public void AScopeThisPageCannotApplyIsDisabledWithTheTermsNamed()
    {
        var control = Control("repositories");

        control.WaitForAssertion(() => Assert.True(Disabled(control, "unpinned")));
        var reason = Reason(control, "unpinned");
        Assert.Contains("Repositories cannot narrow by", reason);
        Assert.Contains("Reference style: tag", reason);
        Assert.Contains("Actions, Relationships and the Explorer can", reason);
    }

    /// And the same scope is offered on the page that applies it.
    [Fact]
    public void AScopeThisPageAppliesIsOffered()
    {
        var control = Control("inventory");

        control.WaitForAssertion(() => Assert.False(Disabled(control, "unpinned")));
        Assert.Equal("", Reason(control, "unpinned"));
    }

    /// <summary>
    /// A scope says what it holds on hover, built-in or kept. "Open Floating references on this
    /// page." said nothing the chip did not.
    /// </summary>
    [Fact]
    public void AScopeSaysWhatItHoldsOnHover()
    {
        var control = Control("inventory");

        control.WaitForAssertion(() => Assert.False(Disabled(control, "unpinned")));
        Assert.Equal("What a workflow references by a tag or branch rather than a commit SHA.",
            control.Find("[data-abom-view='unpinned']").GetAttribute("title"));
        Assert.Equal("Keeps Source: upstream.",
            control.Find("[data-abom-view='upstream-rules']").GetAttribute("title"));
    }

    /// And a scope this page cannot open still says what it holds, before why it is disabled.
    [Fact]
    public void ADisabledScopeSaysWhatItHoldsAndWhyItIsDisabled()
    {
        var control = Control("repositories");

        control.WaitForAssertion(() => Assert.True(Disabled(control, "unpinned")));
        var reason = Reason(control, "unpinned");
        Assert.StartsWith("What a workflow references by a tag or branch rather than a commit SHA.", reason);
        Assert.Contains("Repositories cannot narrow by", reason);
    }

    /// <summary>
    /// A page answering part of a list term refuses the rest member by member, and the member is
    /// named under its term's label: “flags=unanalyzed: ” named neither.
    /// </summary>
    [Fact]
    public void AMemberThisPageRefusesIsNamedUnderItsTermsLabel()
    {
        var control = Control("workflows");

        control.WaitForAssertion(() => Assert.True(Disabled(control, "coverage-gaps")));
        var reason = Reason(control, "coverage-gaps");
        Assert.Contains("“Has: unanalyzed”", reason);
        Assert.DoesNotContain("flags=", reason);
    }

    /// <summary>
    /// A page the server said nothing about is not a page that applies everything. The server
    /// leaves the answer out only for a predicate it can no longer read, which no page can open.
    /// </summary>
    [Fact]
    public void APageTheServerSaidNothingAboutDoesNotOpenTheScope()
    {
        var control = Control("inventory");

        control.WaitForAssertion(() => Assert.True(Disabled(control, "unreadable")));
        Assert.Contains("cannot read", Reason(control, "unreadable"));
    }

    /// The graph refuses a source, so the Explorer does not open a scope carrying one.
    [Fact]
    public void TheExplorerDoesNotOpenAScopeTheGraphRefuses()
    {
        var control = Control("graph");

        control.WaitForAssertion(() => Assert.True(Disabled(control, "upstream-rules")));
        Assert.Contains("The Explorer cannot narrow by", Reason(control, "upstream-rules"));
        Assert.Contains("Actions can", Reason(control, "upstream-rules"));
    }

    /// Choosing a scope hands it to the page, which knows what else loading it means there.
    [Fact]
    public void ChoosingAScopeHandsItToThePage()
    {
        NamedView? chosen = null;
        var control = Control("inventory", chosen: v => chosen = v);

        control.WaitForAssertion(() => control.Find("[data-abom-view='unpinned']").Click());

        Assert.Equal("ref=tag", chosen?.Predicate);
    }

    /// <summary>
    /// A page that draws keeps its picture beside the question. The Explorer's picture was stored
    /// by nothing, because the only Keep on offer sent none.
    /// </summary>
    [Fact]
    public void KeepingSendsThePictureWhenThePageDraws()
    {
        Scope.Set(ViewSpec.Parse("ref=tag"));
        var control = Control("graph", presentation: () => "view=priority&groupBy=repo");

        control.WaitForAssertion(() => control.Find("[data-abom-scope-name]").Input("floats"));
        control.Find("[data-abom-save-scope]").Click();

        control.WaitForAssertion(() =>
        {
            var put = Sent.Last(r => r.Url.Contains("api/views/"));
            Assert.Equal("PUT", put.Method);
            Assert.EndsWith("api/views/floats", put.Url);
            Assert.Contains("\"predicate\":\"ref=tag\"", put.Body);
            Assert.Contains("\"presentation\":\"view=priority\\u0026groupBy=repo\"", put.Body);
        });
    }

    /// A page with nothing to draw keeps no picture, rather than one of every default.
    [Fact]
    public void KeepingSendsNoPictureFromAPageThatDoesNotDraw()
    {
        Scope.Set(ViewSpec.Parse("ref=tag"));
        var control = Control("inventory");

        control.WaitForAssertion(() => control.Find("[data-abom-scope-name]").Input("floats"));
        control.Find("[data-abom-save-scope]").Click();

        control.WaitForAssertion(() => Assert.DoesNotContain(
            "presentation", Sent.Last(r => r.Url.Contains("api/views/")).Body));
    }

    /// <summary>
    /// A kept scope is its predicate. The selection rides in the same address and is not a term,
    /// so sending it would have the server refuse the whole scope as a filter it has never heard of.
    /// </summary>
    [Fact]
    public void KeepingSendsThePredicateAndNeverTheSelection()
    {
        Scope.Set(ViewSpec.Parse("ref=tag"));
        Scope.Select(["actions/checkout"]);
        var control = Control("inventory");

        control.WaitForAssertion(() => control.Find("[data-abom-scope-name]").Input("floats"));
        control.Find("[data-abom-save-scope]").Click();

        control.WaitForAssertion(() =>
        {
            var body = Sent.Last(r => r.Url.Contains("api/views/")).Body;
            Assert.Contains("\"predicate\":\"ref=tag\"", body);
            Assert.DoesNotContain("selection", body);
        });
    }

    /// A refused write is said where the reader is looking, rather than leaving a control that
    /// appears to work over a catalog that never changes.
    [Fact]
    public void AKeepTheServerRefusesSaysSo()
    {
        Fail("api/views/", HttpStatusCode.Forbidden);
        Scope.Set(ViewSpec.Parse("ref=tag"));
        var control = Control("inventory");

        control.WaitForAssertion(() => control.Find("[data-abom-scope-name]").Input("floats"));
        control.Find("[data-abom-save-scope]").Click();

        control.WaitForAssertion(() => Assert.Contains("could not be saved", control.Markup));
    }

    private ScopeService Scope => Services.GetRequiredService<ScopeService>();
}
