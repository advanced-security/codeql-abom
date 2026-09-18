using Abom.WebAssembly.Services;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// "1 nodes" is the interface reporting a number it did not read. Every count beside a noun goes
/// through here rather than being interpolated next to a hard-coded plural, which is how the
/// Explorer's own count line came to disagree with itself at one.
/// </summary>
public class CountTextTests
{
    [Theory]
    [InlineData(0, "0 nodes")]
    [InlineData(1, "1 node")]
    [InlineData(2, "2 nodes")]
    [InlineData(1500, "1,500 nodes")]
    public void ACountAgreesWithTheNounBesideIt(int n, string expected) =>
        Assert.Equal(expected, CountText.Of(n, "node", "nodes"));

    /// <summary>
    /// A count that is typeset apart from its noun — bold, or in its own element — still has to
    /// pick the right one.
    /// </summary>
    [Theory]
    [InlineData(1, "repository")]
    [InlineData(0, "repositories")]
    [InlineData(7, "repositories")]
    public void TheNounCanBeChosenWithoutTheNumber(int n, string expected) =>
        Assert.Equal(expected, CountText.Noun(n, "repository", "repositories"));

    /// <summary>Thousands are grouped, because every other figure on every surface is.</summary>
    [Fact]
    public void ACountIsGroupedTheWayTheRestOfTheInterfaceGroupsOne() =>
        Assert.Equal("11,235", CountText.Number(11235));
}
