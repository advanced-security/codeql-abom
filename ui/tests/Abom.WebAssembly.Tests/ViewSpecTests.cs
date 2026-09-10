using Abom.WebAssembly.Services;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The predicate half of a view, which has to mean exactly the same thing here as it does in Go.
///
/// <para>
/// These are not bUnit tests and deliberately take no <c>BunitContext</c>: the spec is text, and
/// the point is that the text is the contract.
/// </para>
/// </summary>
public class ViewSpecTests
{
    [Fact]
    public void ASpecSurvivesBeingWrittenAndReadBack()
    {
        var want = new ViewSpec
        {
            Scope = "owned",
            Search = "checkout",
            Kinds = ["action", "workflow"],
            Origins = ["seed"],
            Rules = ["abom/unpinned", "actions/missing-workflow-permissions"],
            EdgeClass = ["external"],
            RefStyle = "pinned",
            Boundary = "external",
            MinReach = 5,
            Flags = ["advisory", "findings"],
        };

        var got = ViewSpec.Parse(want.ToQueryString());

        Assert.True(got.Equals(want), $"round trip lost something: {got.ToQueryString()}");
    }

    [Fact]
    public void TheSamePredicateAlwaysWritesTheSameText()
    {
        var a = new ViewSpec { Kinds = ["workflow", "action"], Flags = ["findings", "advisory"] };
        var b = new ViewSpec { Kinds = ["action", "workflow"], Flags = ["advisory", "findings"] };

        Assert.Equal(a.ToQueryString(), b.ToQueryString());
    }

    [Fact]
    public void AnEmptyPredicateWritesNothing()
    {
        Assert.Equal("", new ViewSpec().ToQueryString());
        Assert.True(ViewSpec.Parse("").Equals(new ViewSpec()));
    }

    /// <summary>
    /// A gate that acquired a <c>use3d</c> field would have to ignore it, and a change to what is
    /// selected must not hide behind a change to how it is drawn.
    /// </summary>
    [Theory]
    [InlineData("selection=a/b")]
    [InlineData("hops=2")]
    [InlineData("direction=out")]
    [InlineData("groupBy=owner")]
    [InlineData("collapsed=true")]
    [InlineData("use3d=true")]
    public void PresentationIsNotPartOfThePredicate(string text) =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse(text));

    /// <summary>
    /// A typo that silently matched everything is the worst failure for a gate that fails a pull
    /// request on a non-empty match.
    /// </summary>
    [Fact]
    public void AnUnknownParameterIsRefusedRatherThanIgnored() =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse("kindz=action"));

    [Theory]
    [InlineData("minReach=lots")]
    [InlineData("minReach=-1")]
    public void AMalformedReachIsRefused(string text) =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse(text));

    [Fact]
    public void ARepeatedValueIsOneValue() =>
        Assert.Equal(2, ViewSpec.Parse("kinds=action,action,workflow").Kinds.Count);

    [Fact]
    public void ASearchTermSurvivesCharactersThatWouldBreakTheText()
    {
        var want = new ViewSpec { Search = "owner/repo & things=maybe" };
        Assert.Equal(want.Search, ViewSpec.Parse(want.ToQueryString()).Search);
    }

    /// <summary>
    /// The cross-language contract. Each text below is exactly what Go's <c>view.Spec.String()</c>
    /// produces for the same predicate, asserted verbatim on both sides. If either implementation
    /// drifts, the URL an operator shares means one thing to the browser and another to the API,
    /// and nothing anywhere reports it — so this is the test that has to be exact.
    /// </summary>
    [Theory]
    [InlineData("kinds=action%2Cworkflow")]
    [InlineData("flags=advisory%2Cfindings&scope=owned")]
    [InlineData("minReach=5&q=checkout")]
    [InlineData("boundary=external&edges=external&ref=pinned")]
    [InlineData("origins=seed%2Cupstream&rules=abom%2Funpinned")]
    [InlineData("notRules=abom%2Fnoisy&rules=actions%2F%2A")]
    [InlineData("provider=CodeQL&severity=error")]
    [InlineData("repositories=octo-org%2Fapp%2Cocto-org%2Ftool")]
    public void TheCanonicalTextIsIdenticalToTheOneGoWrites(string canonical) =>
        Assert.Equal(canonical, ViewSpec.Parse(canonical).ToQueryString());

    [Fact]
    public void AnEmptyValueIsRefusedRatherThanTreatedAsAbsent() =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse("kinds="));
}
