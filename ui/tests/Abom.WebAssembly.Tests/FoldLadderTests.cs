using Abom.WebAssembly.Pages;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The fold is a ladder, not a switch.
///
/// Shipped as a switch: the Explorer arrives folded to 603 owner groups, and opening that fold
/// went straight to all 9,811 nodes — the view that renders at under four frames a second and
/// never settles. Every rung has to lead to the next one.
/// </summary>
public class FoldLadderTests
{
    [Fact]
    public void OpeningTheOwnerFoldStepsToRepositoriesRatherThanTheWholeGraph()
    {
        Assert.Equal("repo", Explorer.FinerFold("owner"));
    }

    [Fact]
    public void EachRungLeadsToTheNext()
    {
        Assert.Equal("workflow", Explorer.FinerFold("repo"));
    }

    // Only the last rung may open the whole graph, and it has to be reachable or the finest
    // grouping would be a dead end a reader could never get past.
    [Fact]
    public void TheLastRungOpensOntoTheWholeGraph()
    {
        Assert.Null(Explorer.FinerFold("workflow"));
    }

    // "none" is what the grouping selector calls no grouping at all; it is not on the ladder and
    // must not be treated as a rung with a successor.
    [Fact]
    public void AGroupingThatIsNotOnTheLadderHasNoNextRung()
    {
        Assert.Null(Explorer.FinerFold("none"));
        Assert.Null(Explorer.FinerFold(""));
    }

    // The ladder runs coarsest to finest. Reversed, opening a fold would bury the reader deeper.
    [Fact]
    public void TheLadderRunsCoarsestFirst()
    {
        Assert.Equal(["owner", "repo", "workflow"], Explorer.FoldLadder);
    }
}
