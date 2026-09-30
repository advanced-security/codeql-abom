using Abom.WebAssembly.Services;
using AngleSharp.Html.Parser;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class QueryHelpTests
{
    [Fact]
    public void RecordedDocumentationRendersStructureWithoutActiveContent()
    {
        var html = QueryHelpHtml.Render("""
            # Recommendation
            Use `env.VALUE`.

            <script>alert(1)</script>
            <img src="https://example.com/tracker" onerror="alert(1)">

            [unsafe](javascript:alert%281%29)
            [reference](https://example.com/docs)
            ![remote image](https://example.com/image.png)
            """);
        var document = new HtmlParser().ParseDocument(html);
        Assert.Equal("Recommendation", document.QuerySelector("h1")?.TextContent);
        Assert.Equal("env.VALUE", document.QuerySelector("code")?.TextContent);
        Assert.Empty(document.QuerySelectorAll("script,img,iframe,style"));
        foreach (var link in document.QuerySelectorAll("a"))
        {
            Assert.StartsWith("https://", link.GetAttribute("href"));
            Assert.Contains("noopener", link.GetAttribute("rel"));
        }
    }

    [Fact]
    public void ResultLocationLinksOnlyTargetRecordedLocations()
    {
        var html = QueryHelpHtml.Render("[source](1) and [missing](2)",
            relatedLocations: new HashSet<int> { 1 }, locationPrefix: "finding-7");
        var document = new HtmlParser().ParseDocument(html);
        var link = Assert.Single(document.QuerySelectorAll("a"));
        Assert.Equal("#finding-7-location-1", link.GetAttribute("href"));
        Assert.Contains("missing", document.Body!.TextContent);
    }
}