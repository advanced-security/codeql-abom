using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Four surfaces, not nine. A page per question has to invent its own scope, and the questions
/// that are really predicates now live as tabs on Inventory.
/// </summary>
public class NavMenuTests : AbomTestContext
{
    [Fact]
    public void TheNavOffersTheFourSurfacesAndNothingElse()
    {
        var nav = Render<Layout.NavMenu>();

        var hrefs = nav.FindAll("a").Select(a => a.GetAttribute("href") ?? "").ToArray();
        Assert.Equal(["/", "/inventory", "/explore", "/settings"], hrefs);
    }

    /// <summary>
    /// A question that is the same scope in a different shape is a unit, a tab or an action — not a
    /// page. The bill of materials is the one page left that is not a surface, and it is deliberately
    /// not a fifth nav entry: see plans/ui/inventory-units.md.
    /// </summary>
    [Theory]
    [InlineData("/bom")]
    public void AQuestionThatIsNowPartOfASurfaceIsNotAlsoANavEntry(string href)
    {
        var nav = Render<Layout.NavMenu>();

        Assert.Empty(nav.FindAll($"a[href='{href}']"));
    }
}
