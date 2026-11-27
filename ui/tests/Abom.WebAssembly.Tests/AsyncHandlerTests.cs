using System.Text.RegularExpressions;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class AsyncHandlerTests
{
    // An event handler is async void, so nothing awaits it and an exception it lets out takes the
    // interface down. One still loading when its page goes away meets a disposed HttpClient: that
    // ended a whole bUnit run, and in the browser it is the error banner on a page already left.
    [Fact]
    public void EveryAsyncVoidHandlerOutlivesItsPage()
    {
        var root = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "../../../../.."));
        var signature = new Regex(@"\basync\s+void\s+(\w+)\s*\([^)]*\)\s*\{");
        var exposed = new List<string>();
        foreach (var file in new[] { "Shared", "Pages", "Services" }
                     .SelectMany(d => Directory.EnumerateFiles(Path.Combine(root, d), "*.*"))
                     .Where(f => f.EndsWith(".razor", StringComparison.Ordinal) || f.EndsWith(".cs", StringComparison.Ordinal)))
        {
            var source = File.ReadAllText(file);
            foreach (Match m in signature.Matches(source))
            {
                if (!Body(source, m.Index + m.Length - 1).Contains("catch (ObjectDisposedException", StringComparison.Ordinal))
                {
                    exposed.Add($"{Path.GetFileName(file)}: {m.Groups[1].Value}");
                }
            }
        }

        Assert.Empty(exposed);
    }

    private static string Body(string source, int open)
    {
        var depth = 0;
        for (var i = open; i < source.Length; i++)
        {
            depth += source[i] switch { '{' => 1, '}' => -1, _ => 0 };
            if (depth == 0)
            {
                return source[open..(i + 1)];
            }
        }
        return source[open..];
    }
}
