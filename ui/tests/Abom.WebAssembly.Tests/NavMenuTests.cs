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
}
