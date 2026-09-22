using Abom.WebAssembly.Models;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The two halves of a view travel together to the server and stay apart in meaning: the
/// predicate is the question, the presentation is the picture. A surface that cannot draw saves
/// no picture rather than a default one — Inventory is a list, and a list has no grouping, no
/// depth and no third dimension to record.
/// </summary>
public sealed class SavedViewPresentationTests : AbomTestContext
{
    /// The picture is sent beside the question, not folded into it.
    [Fact]
    public async Task SavingSendsThePresentationBesideThePredicate()
    {
        Answer("api/views", "[]");

        var api = Services.GetService<Services.AbomApi>()!;
        await api.SaveViewAsync("mine", "scope=owned", "view=priority&groupBy=owner");

        var write = Assert.Single(Sent, r => r.Url.Contains("api/views/mine"));
        Assert.Contains("\"predicate\":\"scope=owned\"", write.Body);
        Assert.Contains("\"presentation\":\"view=priority\\u0026groupBy=owner\"", write.Body);
    }

    /// <summary>
    /// A surface with nothing to draw sends no presentation at all. Sending an empty one would
    /// record "drawn with every default", which is a claim about a picture that was never on
    /// screen and would reset the drawing of whoever applied the view.
    /// </summary>
    [Fact]
    public async Task ASurfaceThatDoesNotDrawSavesNoPresentation()
    {
        Answer("api/views", "[]");

        var api = Services.GetService<Services.AbomApi>()!;
        await api.SaveViewAsync("mine", "scope=owned");

        var write = Assert.Single(Sent, r => r.Url.Contains("api/views/mine"));
        Assert.DoesNotContain("presentation", write.Body);
    }

    /// The catalog carries the picture back, or applying a saved view can only restore half of it.
    [Fact]
    public async Task TheCatalogCarriesThePresentationBack()
    {
        Answer("api/views", """
            [{"name":"drawn","predicate":"scope=owned","presentation":"view=priority","builtin":false}]
            """);

        var views = await Services.GetService<Services.AbomApi>()!.GetViewsAsync();

        Assert.Equal("view=priority", Assert.Single(views).Presentation);
    }
}
