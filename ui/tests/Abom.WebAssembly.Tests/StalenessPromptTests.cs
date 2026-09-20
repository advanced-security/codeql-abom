using Abom.WebAssembly.Models;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// What opens the removal modal. Removal is undone only by re-analysis, so the rule that decides
/// to ask is stated and tested on its own rather than only inside a page.
/// </summary>
public class StalenessPromptTests
{
    private static Staleness Found(int nodes) => new()
    {
        Checked = true,
        CheckedAt = "2026-09-18T19:34:27Z",
        View = new PruneView
        {
            Declared = 25,
            Reachable = 229,
            Removed = new PruneRemoval { Nodes = nodes },
        },
    };

    /// <summary>
    /// Nothing has looked yet, which is not the same answer as nothing to remove. Asking on it
    /// would put a destructive offer in front of somebody on no evidence at all.
    /// </summary>
    [Fact]
    public void ACheckThatHasNotRunDoesNotAsk() =>
        Assert.False(new Staleness().HasSomethingToRemove);

    /// <summary>A measured corpus with nothing outside the closure is left alone.</summary>
    [Fact]
    public void ACheckThatFoundNothingDoesNotAsk() =>
        Assert.False(Found(0).HasSomethingToRemove);

    /// <summary>
    /// Declared, but nothing enumerated under it — the state every new install passes through.
    /// Offering to remove from a corpus nothing could be measured against reads as a corpus that
    /// was examined and found wanting.
    /// </summary>
    [Fact]
    public void ACheckThatCouldNotMeasureDoesNotAsk()
    {
        var stale = Found(12) with { Unavailable = "Nothing has been enumerated yet." };

        Assert.False(stale.HasSomethingToRemove);
    }

    /// <summary>And when there is something, it is raised rather than left to be found.</summary>
    [Fact]
    public void ACheckThatFoundSomethingAsks() =>
        Assert.True(Found(1).HasSomethingToRemove);
}
