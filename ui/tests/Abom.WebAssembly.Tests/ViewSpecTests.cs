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

    /// <summary>
    /// Scoping to a business unit is written as a custom property, and this class could not say it
    /// at all: <c>properties</c> parsed as an unknown filter, so the one term the interface needs
    /// to name an application or a team was the one it refused. Go has carried it since the term
    /// was added.
    /// </summary>
    [Fact]
    public void APropertyScopeSurvivesBeingWrittenAndReadBack()
    {
        var want = new ViewSpec
        {
            Scope = "owned",
            Properties = [new("business_id", "3"), new("team", "core")],
            NotProperties = [new("tier", "experimental")],
        };

        var got = ViewSpec.Parse(want.ToQueryString());

        Assert.True(got.Equals(want), $"round trip lost something: {got.ToQueryString()}");
    }

    /// <summary>
    /// One parameter per match rather than a comma-joined list: a property value is operator text,
    /// and a value containing a comma must not silently become two values.
    /// </summary>
    [Fact]
    public void EachPropertyMatchIsItsOwnParameter()
    {
        var spec = new ViewSpec
        {
            Properties = [new("team", "core"), new("business_id", "3")],
        };

        Assert.Equal("properties=business_id%3A3&properties=team%3Acore", spec.ToQueryString());
    }

    [Fact]
    public void APropertyValueMayContainAComma()
    {
        var want = new ViewSpec { Properties = [new("owners", "core,platform")] };

        var got = ViewSpec.Parse(want.ToQueryString());

        var match = Assert.Single(got.Properties);
        Assert.Equal("core,platform", match.Value);
    }

    /// <summary>
    /// Half a match would filter against whichever property the server happened to be configured
    /// with, which is the silent widening the predicate language exists to prevent.
    /// </summary>
    [Theory]
    [InlineData("properties=3")]
    [InlineData("properties=:3")]
    [InlineData("properties=business_id:")]
    [InlineData("notProperties=tier")]
    public void HalfAPropertyMatchIsRefused(string text) =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse(text));

    /// <summary>A value may itself contain the separator, so only the first one divides.</summary>
    [Fact]
    public void OnlyTheFirstSeparatorDividesAPropertyTerm()
    {
        var got = ViewSpec.Parse("properties=owner%3Aocto-org%3Aplatform");

        var match = Assert.Single(got.Properties);
        Assert.Equal("owner", match.Name);
        Assert.Equal("octo-org:platform", match.Value);
    }

    /// <summary>
    /// Go encodes a space as <c>+</c> rather than <c>%20</c>, and the text has to be byte-identical
    /// across the two because the same string is a storage key on one side and a link on the other.
    /// </summary>
    /// <remarks>
    /// The expected text is not hand-derived: it was read back from <c>GET /api/views</c> after Go
    /// canonicalized it on write, so this is the two implementations compared rather than one
    /// implementation compared with a reading of the other.
    /// </remarks>
    [Fact]
    public void ThePropertyTextIsByteIdenticalToWhatGoWrites()
    {
        var spec = new ViewSpec
        {
            Scope = "owned",
            Properties = [new("app_name", "Billing, Payments and Fraud"), new("business_id", "3")],
        };

        Assert.Equal(
            "properties=app_name%3ABilling%2C+Payments+and+Fraud&properties=business_id%3A3&scope=owned",
            spec.ToQueryString());
        Assert.Equal("Billing, Payments and Fraud",
            ViewSpec.Parse(spec.ToQueryString()).Properties[0].Value);
    }

    /// <summary>The canonical text is a storage key, so the order they were typed in cannot show.</summary>
    [Fact]
    public void PropertyMatchesAreSortedAndDeduplicated()
    {
        var a = new ViewSpec
        {
            Properties = [new("team", "core"), new("business_id", "3"), new("team", "core")],
        };
        var b = new ViewSpec { Properties = [new("business_id", "3"), new("team", "core")] };

        Assert.Equal(b.ToQueryString(), a.ToQueryString());
    }

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
