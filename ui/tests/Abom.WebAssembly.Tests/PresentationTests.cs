using Abom.WebAssembly.Models;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A saved view is half a bookmark without this. The predicate brings the question back; the
/// presentation brings the picture back, and a graph redrawn as a different view of the same
/// nodes answers the same question while showing something a reader did not save.
///
/// <para>It is deliberately not part of <c>ViewSpec</c>. Folding <c>view</c> or <c>groupBy</c>
/// into the filter language would make the server responsible for browser rendering, and would
/// pad every listing's refusal list with terms that are not filters. So it round-trips as its own
/// text, and nothing outside the Explorer reads it.</para>
/// </summary>
public sealed class PresentationTests
{
    /// Everything the Explorer decides about drawing survives the round trip, or a saved view
    /// comes back looking like a different one.
    [Fact]
    public void EveryDrawingChoiceSurvivesTheRoundTrip()
    {
        var drawn = new Presentation
        {
            View = "priority",
            GroupBy = "owner",
            Hops = 2,
            Direction = "out",
            Use3d = true,
            Collapsed = true,
            StableLayout = true,
            Selection = "actions/checkout@v4",
        };

        Assert.Equal(drawn, Presentation.Parse(drawn.ToQueryString()));
    }

    /// <summary>
    /// A choice left at its default is written as absent. A presentation that spelled out every
    /// default would claim its author decided each one, and a view saved before a knob existed
    /// would become indistinguishable from one saved with that knob turned off.
    /// </summary>
    [Fact]
    public void ADefaultIsAbsentRatherThanSpelledOut()
    {
        Assert.Equal(string.Empty, new Presentation().ToQueryString());
    }

    /// <summary>
    /// Nothing saved means "draw it however you would anyway", which is not the same as a
    /// presentation whose every value happens to be the default. Only the first leaves a reader's
    /// current choices alone.
    /// </summary>
    [Fact]
    public void NothingSavedIsNotTheSameAsEveryDefault()
    {
        Assert.False(Presentation.Parse("").HasAny);
        Assert.True(Presentation.Parse("view=priority").HasAny);
    }

    /// <summary>
    /// A predicate term that wandered in is ignored rather than read as a drawing choice. The two
    /// halves travel in one address, and a presentation that swallowed <c>scope</c> would let a
    /// saved view silently renarrow the question when it was applied.
    /// </summary>
    [Fact]
    public void AFilterTermIsNotReadAsADrawingChoice()
    {
        var drawn = Presentation.Parse("scope=owned&view=priority&flags=findings");

        Assert.Equal("priority", drawn.View);
        Assert.DoesNotContain("scope", drawn.ToQueryString());
        Assert.DoesNotContain("flags", drawn.ToQueryString());
    }

    /// A hop count that is not a number is nobody's choice, not zero hops.
    [Fact]
    public void AHopCountThatIsNotANumberIsNotReadAsZero()
    {
        Assert.Equal(new Presentation().Hops, Presentation.Parse("hops=lots").Hops);
    }
}
