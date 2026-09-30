using Markdig;
using Markdig.Renderers.Html;
using Markdig.Syntax;
using Markdig.Syntax.Inlines;

namespace Abom.WebAssembly.Services;

public static class QueryHelpHtml
{
    private static readonly MarkdownPipeline Pipeline = new MarkdownPipelineBuilder()
        .DisableHtml().UsePipeTables().Build();

    public static string Render(string text, string? baseUri = null,
        IReadOnlySet<int>? relatedLocations = null, string locationPrefix = "finding")
    {
        var document = Markdown.Parse(text, Pipeline);
        foreach (var link in document.Descendants<LinkInline>().ToArray())
        {
            var target = int.TryParse(link.Url, out var location) && relatedLocations?.Contains(location) == true
                ? $"#{locationPrefix}-location-{location}"
                : SafeUrl(link.Url, baseUri);
            if (target is null)
            {
                link.ReplaceBy(new LiteralInline(string.Concat(
                    link.Descendants<LiteralInline>().Select(part => part.Content.ToString()))));
                continue;
            }
            link.IsImage = false;
            link.Url = target;
            if (!target.StartsWith('#'))
            {
                link.GetAttributes().AddProperty("target", "_blank");
                link.GetAttributes().AddProperty("rel", "noopener noreferrer");
            }
        }
        foreach (var link in document.Descendants<AutolinkInline>().ToArray())
        {
            var target = SafeUrl(link.Url);
            if (target is null)
            {
                link.ReplaceBy(new LiteralInline(link.Url));
                continue;
            }
            link.Url = target;
            link.GetAttributes().AddProperty("target", "_blank");
            link.GetAttributes().AddProperty("rel", "noopener noreferrer");
        }
        return Markdown.ToHtml(document, Pipeline);
    }

    public static string? SafeUrl(string? value, string? baseUri = null)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri)
            && Uri.TryCreate(baseUri, UriKind.Absolute, out var basis))
        {
            Uri.TryCreate(basis, value, out uri);
        }
        return uri?.Scheme is "https" or "http" ? uri.AbsoluteUri : null;
    }
}