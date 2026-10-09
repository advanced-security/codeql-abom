using System.Linq;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The last three claims the four-surfaces work owed a reader: where a findings term matched when
/// it matched nothing here, which tabs this surface cannot draw, and that the bill of materials is
/// an action on the scope rather than a page with a scope of its own.
/// </summary>
public class InventoryHonestyTests : AbomTestContext
{
    private const string Views = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true,
          "refuses":{"inventory":[],"relationships":[],"graph":[]}},
         {"name":"crossings","predicate":"boundary=cross-owner","builtin":false,
          "refuses":{"inventory":["boundary"],"relationships":[],"graph":[]}}]
        """;

    public InventoryHonestyTests() => Answer("api/views", Views);

    private void Go(string url) =>
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo(url);

    /// <summary>
    /// A findings question that matches nothing among the kinds on screen answers zero, and on the
    /// real corpus that is the normal case for the most important rule there is: all 277
    /// <c>actions/unpinned-tag</c> findings sit on step and job nodes. Zero then reads as "nothing
    /// is wrong" when it means "measured somewhere you are not looking".
    /// </summary>
    [Fact]
    public void AZeroThatMeansMeasuredElsewhereSaysWhereItMatched()
    {
        Answer("api/actions", """
            {"items":[],"total":0,"elsewhere":[{"kind":"step","count":210},{"kind":"job","count":9}]}
            """);
        Go("/inventory?rules=actions%2Funpinned-tag");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var text = System.Text.RegularExpressions.Regex.Replace(page.Markup, @"\s+", " ");
            Assert.Contains("210", text);
            Assert.Contains("step", text);
            Assert.Contains("9", text);
            Assert.Contains("job", text);
        });
    }

    /// <summary>
    /// And the sentence stays away when the whole match is on screen, or a reader learns to skip it
    /// by the time it matters.
    /// </summary>
    [Fact]
    public void NothingIsSaidWhenTheWholeMatchIsOnScreen()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory?rules=actions%2Funpinned-tag");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        Assert.DoesNotContain("elsewhere", page.Markup, System.StringComparison.OrdinalIgnoreCase);
        Assert.Empty(page.FindAll("[data-abom-unsourced]"));
    }

    /// <summary>
    /// A source answers only for findings whose evaluation recorded one. Narrowed by source, a
    /// listing says how many rows carry matching findings with none recorded, or zero among ABOM's
    /// own rules reads as "they found nothing" on a corpus where none were ever attributed.
    /// </summary>
    [Fact]
    public void ASourceSaysHowManyRowsItCouldNotAnswerFor()
    {
        Answer("api/actions", """{"items":[],"total":0,"unsourced":12}""");
        Go("/inventory?source=abom");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.StartsWith("12 more dependencies carry",
            page.Find("[data-abom-unsourced]").TextContent.Trim()));
    }

    /// <summary>
    /// A provider answers only for findings that recorded one, which on the crawled organization
    /// is 1,641 of 12,317. Narrowed by the only provider on offer, the listing says how many rows it
    /// could not speak for rather than presenting an eighth of the corpus as the whole.
    /// </summary>
    [Fact]
    public void AProviderSaysHowManyRowsItCouldNotAnswerFor()
    {
        Answer("api/actions", """{"items":[],"total":0,"unprovided":7}""");
        Go("/inventory?provider=CodeQL");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.StartsWith("7 more dependencies carry",
            page.Find("[data-abom-unprovided]").TextContent.Trim()));
    }

    /// <summary>
    /// A view that will refuse itself says so before it is chosen. The refusal that arrives after
    /// is correct and one click too late.
    /// </summary>
    [Fact]
    public void AViewThisSurfaceCannotDrawIsMarkedBeforeItIsChosen()
    {
        Answer("api/filters", """
            [{"name":"boundary","label":"Boundary","help":"Which side of ownership",
              "arity":"scalar","surfaces":["graph","relationships"]}]
            """);
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            // The reason, not a framework class name: what a reader needs is which term this
            // surface cannot apply and where it is answered.
            var reason = Tip(page, "crossings");
            Assert.Contains("Boundary: cross-owner", reason);
            Assert.Contains("Relationships and the Explorer can", reason);
        });
        Assert.DoesNotContain("cannot", Tip(page, "owned"));
    }

    private static string Tip(IRenderedComponent<Pages.Inventory> page, string view) =>
        page.Find($"[data-abom-view='{view}']").Closest("[data-abom-tip]")?.GetAttribute("data-abom-tip") ?? "";

    /// <summary>
    /// The document is a rendering of the scope, exported from the scope. A link that dropped it
    /// would export a bill of materials about the whole organization from a narrowed screen.
    /// </summary>
    [Fact]
    public void TheBillOfMaterialsCarriesTheScopeItDescribes()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory?properties=business_id%3A3");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(
            "properties=business_id%3A3",
            page.Find("[data-abom-action='bom-cyclonedx']").GetAttribute("href") ?? ""));
    }

    /// <summary>
    /// Arriving at Inventory from Inventory is still arriving. Blazor keeps the component when the
    /// route does not change, so the address said one thing and the grid went on showing the
    /// answer to the last one — the escape link out of an "it matched elsewhere" zero landed on
    /// another zero, with the rows sitting behind the very URL in the address bar. Browser Back
    /// was broken by the same gap.
    /// </summary>
    [Fact]
    public void ANewScopeAtTheSameRouteIsStillReadAndReloaded()
    {
        Answer("api/actions", """{"items":[],"total":0}""");
        Go("/inventory?rules=actions%2Funpinned-tag");

        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));

        Go("/inventory?kinds=step,job&rules=actions%2Funpinned-tag");

        // Canonical, so sorted: the serializer orders list values and two spellings of one question
        // have to compare equal.
        page.WaitForAssertion(() => Assert.Contains(
            "kinds=job%2Cstep", Sent.Last(r => r.Url.Contains("api/actions")).Url));
    }
}
