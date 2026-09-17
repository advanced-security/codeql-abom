using System.Linq;
using System.Net;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A scope an operator kept. <c>abom_saved_view</c> has held named predicates, and the API has
/// served them, since before any of this; what reached the table was a hand-edited URL and nothing
/// else. These pin the half that makes the catalog an operator's rather than only the product's.
/// </summary>
public class SavedScopeTests : AbomTestContext
{
    private const string Builtins = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]
        """;

    private const string WithSaved = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true},
         {"name":"billing-floats","predicate":"properties=business_id%3A3&ref=tag",
          "builtin":false,"createdBy":"interface"}]
        """;

    private const string Page = """{"items":[],"total":0}""";

    public SavedScopeTests()
    {
        Answer("api/views", Builtins);
        Answer("api/actions", Page);
    }

    private void Go(string url) =>
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo(url);

    private IRenderedComponent<Pages.Inventory> Inventory()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        return page;
    }

    /// <summary>
    /// Naming the empty scope would store "everything" as a question, and it would sit in the tab
    /// strip beside real ones looking like a decision somebody made.
    /// </summary>
    [Fact]
    public void NothingToNameIsNotSavable()
    {
        Go("/inventory");

        var page = Inventory();

        Assert.True(page.Find("[data-abom-save-scope]").HasAttribute("disabled"));
    }

    [Fact]
    public void AScopeOnScreenCanBeNamedAndKept()
    {
        Go("/inventory?properties=business_id%3A3&ref=tag");

        var page = Inventory();
        page.Find("[data-abom-scope-name]").Input("billing-floats");
        page.Find("[data-abom-save-scope]").Click();

        page.WaitForAssertion(() =>
        {
            var put = Sent.Last(r => r.Url.Contains("api/views/"));
            Assert.Equal("PUT", put.Method);
            Assert.EndsWith("api/views/billing-floats", put.Url);
            Assert.Contains("properties=business_id%3A3", put.Body);
            Assert.Contains("ref=tag", put.Body);
        });
    }

    /// <summary>
    /// The predicate is what is stored, never the name of the tab it came from: a saved view that
    /// recorded "whatever `owned` means" would change meaning when the built-in did.
    /// </summary>
    [Fact]
    public void WhatIsStoredIsThePredicateRatherThanTheTabItCameFrom()
    {
        Go("/inventory?scope=owned");

        var page = Inventory();
        page.Find("[data-abom-scope-name]").Input("mine");
        page.Find("[data-abom-save-scope]").Click();

        page.WaitForAssertion(() =>
            Assert.Contains("scope=owned", Sent.Last(r => r.Url.Contains("api/views/")).Body));
    }

    /// <summary>
    /// A saved view is the operator's to remove. A built-in is not: it is code, the server refuses
    /// to delete it, and offering the control would be an affordance that cannot work.
    /// </summary>
    [Fact]
    public void OnlyASavedTabOffersRemoval()
    {
        Answer("api/views", WithSaved);
        Go("/inventory");

        var page = Inventory();

        Assert.NotNull(page.Find("[data-abom-forget-view='billing-floats']"));
        Assert.Empty(page.FindAll("[data-abom-forget-view='owned']"));
    }

    [Fact]
    public void ForgettingASavedScopeAsksTheServerToDeleteIt()
    {
        Answer("api/views", WithSaved);
        Go("/inventory");

        var page = Inventory();
        page.Find("[data-abom-forget-view='billing-floats']").Click();

        page.WaitForAssertion(() =>
        {
            var gone = Sent.Last(r => r.Url.Contains("api/views/"));
            Assert.Equal("DELETE", gone.Method);
            Assert.EndsWith("api/views/billing-floats", gone.Url);
        });
    }

    /// <summary>
    /// A refused write is reported where the reader is looking. Swallowing it leaves a control that
    /// appears to work and a catalog that never changes, which is worse than saying no.
    /// </summary>
    [Fact]
    public void AServerThatRefusesTheWriteSaysSoOnScreen()
    {
        Fail("api/views/", HttpStatusCode.Forbidden);
        Go("/inventory?ref=tag");

        var page = Inventory();
        page.Find("[data-abom-scope-name]").Input("floats");
        page.Find("[data-abom-save-scope]").Click();

        page.WaitForAssertion(() => Assert.Contains("could not be saved", page.Markup));
    }
}
