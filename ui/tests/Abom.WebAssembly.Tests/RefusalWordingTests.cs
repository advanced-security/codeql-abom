using System;
using System.Linq;
using Abom.WebAssembly.Services;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The browser's parser refuses in its own words, and those words go straight into the alert at
/// the top of the Explorer. `view:` is a namespace out of the Go package this mirrors — a reader
/// who meets it has been handed a stack frame in place of a sentence.
/// </summary>
public class RefusalWordingTests
{
    public static TheoryData<string> Refusals =>
    [
        "?kindz=action",
        "?kinds=",
        "?minReach=lots",
        "?steps=perhaps",
        "?properties=business_id",
    ];

    [Theory]
    [MemberData(nameof(Refusals))]
    public void ARefusalCarriesNoGoNamespace(string address)
    {
        var refusal = Assert.Throws<FormatException>(() => ViewSpec.Parse(address));

        Assert.DoesNotContain("view:", refusal.Message, StringComparison.Ordinal);
    }

    /// <summary>
    /// And it still names what it refused, which is the floor a redirect is built on.
    /// </summary>
    [Fact]
    public void ARefusalStillNamesTheTermItRefused()
    {
        var refusal = Assert.Throws<FormatException>(() => ViewSpec.Parse("?kindz=action"));

        Assert.Contains("kindz", refusal.Message, StringComparison.Ordinal);
    }
}

/// <summary>
/// The browser parses the same language the server does, so a value one of them refuses and the
/// other keeps is a link that means two different things depending on where it is opened.
///
/// These are the terms whose values are fixed in code rather than read from the corpus. An
/// unrecognized one used to be kept and then silently filter nothing.
/// </summary>
public class VocabularyTests
{
    public static TheoryData<string> Outside =>
    [
        "?boundary=nonsense",
        "?boundary=crossOwner",
        "?scope=ownd",
        "?scope=everything",
        "?ref=commit",
    ];

    [Theory]
    [MemberData(nameof(Outside))]
    public void AValueOutsideATermsVocabularyIsRefused(string address) =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse(address));

    public static TheoryData<string> Inside =>
    [
        "?boundary=same-repository",
        "?boundary=same-owner",
        "?boundary=cross-owner",
        "?scope=owned",
        "?scope=exposure",
        "?ref=any",
        "?ref=sha",
        "?ref=tag",
    ];

    [Theory]
    [MemberData(nameof(Inside))]
    public void EveryValueATermAcceptsIsStillAccepted(string address) =>
        Assert.Equal(address.TrimStart('?'), ViewSpec.Parse(address).ToQueryString());

    /// <summary>
    /// The near-misses are real: the canvas draws <c>crossOwner</c> while the store spells it
    /// <c>cross-owner</c>, so a refusal that does not list the alternatives leaves a reader
    /// guessing between two spellings they have both seen.
    /// </summary>
    [Fact]
    public void ARefusedValueNamesTheOnesThatWouldHaveWorked()
    {
        var refusal = Assert.Throws<FormatException>(() => ViewSpec.Parse("?boundary=crossOwner"));

        Assert.Contains("boundary", refusal.Message, StringComparison.Ordinal);
        Assert.Contains("cross-owner", refusal.Message, StringComparison.Ordinal);
    }
}
