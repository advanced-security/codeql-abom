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
            EdgeClass = ["crossOwner"],
            EdgeKinds = ["job-needs-job", "step-uses-action"],
            RefStyle = "sha",
            Boundary = "cross-owner",
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
    [InlineData("boundary=cross-owner&edges=crossOwner&ref=sha")]
    [InlineData("origins=seed%2Cupstream&rules=abom%2Funpinned")]
    [InlineData("notRules=abom%2Fnoisy&rules=actions%2F%2A")]
    [InlineData("provider=CodeQL&severity=error")]
    [InlineData("rules=actions%2F%2A&source=abom%2Cupstream")]
    [InlineData("repositories=octo-org%2Fapp%2Cocto-org%2Ftool")]
    [InlineData("mode=extended")]
    [InlineData("mode=regular&rules=actions%2F%2A")]
    [InlineData("codeScanning=add%2Cenable")]
    public void TheCanonicalTextIsIdenticalToTheOneGoWrites(string canonical) =>
        Assert.Equal(canonical, ViewSpec.Parse(canonical).ToQueryString());

    /// <summary>
    /// The text Go writes for a spec carrying every term, read from the file Go's own test pins.
    /// Two hand-kept lists let <c>codeScanning</c> into the filter picker while this class could not
    /// parse it, and choosing it took the whole interface down with an unhandled exception.
    /// </summary>
    [Fact]
    public void EveryTermGoWritesIsOneThisReadsAndWritesBackIdentically()
    {
        var text = File.ReadAllText(Path.Join(AppContext.BaseDirectory, "testdata", "every-term.txt")).Trim();
        Assert.Equal(text, ViewSpec.Parse(text).ToQueryString());
    }

    /// <summary>
    /// A shape outside the vocabulary is refused rather than dropped. `mixed` is the one that
    /// matters: the store has its own word for a union across both shapes, and a shared link that
    /// could ask for one would turn a deliberate internal decision into a term anybody can spell.
    /// </summary>
    [Theory]
    [InlineData("mode=mixed")]
    [InlineData("mode=full")]
    public void AShapeNothingIsBuiltInIsRefused(string text) =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse(text));

    [Fact]
    public void AnEmptyValueIsRefusedRatherThanTreatedAsAbsent() =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse("kinds="));

    /// <summary>
    /// Where a finding's query came from is a closed list, the one the store records. A source it
    /// cannot produce would match nothing and read as "none of those", so it is refused, and one
    /// bad member refuses the term rather than being dropped from it.
    /// </summary>
    [Theory]
    [InlineData("source=user")]
    [InlineData("source=upstream%2Cgithub")]
    public void ASourceABOMDoesNotKnowIsRefused(string text) =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse(text));

    [Fact]
    public void EverySourceTheStoreRecordsIsReadBack()
    {
        var spec = ViewSpec.Parse("source=upstream%2Cmixed%2Ccustom%2Cabom");
        Assert.Equal(["abom", "custom", "mixed", "upstream"], spec.Sources);
        Assert.Equal("source=abom%2Ccustom%2Cmixed%2Cupstream", spec.ToQueryString());
    }

    /// <summary>
    /// The same refusal Go makes for a list whose members are fixed in code, or a link the API
    /// refuses opens here narrowed to nothing. The store's <c>cross-owner</c> is the near-miss that
    /// matters: it is <c>boundary</c>'s word, and <c>edges</c> says <c>crossOwner</c>.
    /// </summary>
    [Theory]
    [InlineData("kinds=banana")]
    [InlineData("kinds=action%2Cbanana")]
    [InlineData("origins=yours")]
    [InlineData("flags=pinned")]
    [InlineData("edges=cross-owner")]
    [InlineData("edgeKinds=uses")]
    [InlineData("codeScanning=covered%2Cbogus")]
    public void AListMemberOutsideItsVocabularyIsRefused(string text) =>
        Assert.Throws<FormatException>(() => ViewSpec.Parse(text));

    /// <summary>
    /// What you own and what it runs are two scopes with two spellings, read the way Go reads them,
    /// or a link to the supply chain opens here refused.
    /// </summary>
    [Theory]
    [InlineData("scope=owned")]
    [InlineData("scope=supply-chain")]
    [InlineData("scope=exposure")]
    public void EveryScopeTheLanguageDeclaresIsReadBack(string canonical) =>
        Assert.Equal(canonical, ViewSpec.Parse(canonical).ToQueryString());

    [Theory]
    [InlineData("kinds=action%2Ccomposite_action%2Cdocker_image%2Cjob%2Corg%2Crepo%2Creusable_workflow%2Cstep%2Cworkflow")]
    [InlineData("origins=external%2Cowned%2Cseed%2Cupstream")]
    [InlineData("flags=advisory%2Cfindings%2Cunanalyzed")]
    [InlineData("edges=crossOwner%2CsameOwner%2CsameRepo")]
    [InlineData("edgeKinds=composite-step-uses-action%2Cjob-needs-job%2Cjob-uses-reusable-workflow%2Cstep-uses-action")]
    public void EveryListMemberTheGraphProducesIsReadBack(string canonical) =>
        Assert.Equal(canonical, ViewSpec.Parse(canonical).ToQueryString());

    /// <summary>
    /// A band is asked for as Go writes it, and one the language does not have is refused: a SARIF
    /// level is <c>severity</c>'s vocabulary, not this one's.
    /// </summary>
    [Fact]
    public void ASecuritySeverityTermCarriesBandsAsGoWritesThem()
    {
        var spec = ViewSpec.Parse("securitySeverity=high%2Ccritical");
        Assert.Equal(["critical", "high"], spec.SecuritySeverities);
        Assert.Equal("securitySeverity=critical%2Chigh", spec.ToQueryString());
        Assert.Equal("securitySeverity=unknown",
            ViewSpec.Parse("securitySeverity=unknown").ToQueryString());
        Assert.Throws<FormatException>(() => ViewSpec.Parse("securitySeverity=error"));
    }

    /// <summary>
    /// The policy language's terms travel through every page as Go writes them. Refused here, an
    /// advisories tab narrowed by severity would not open at all.
    /// </summary>
    [Fact]
    public void ThePolicyLanguageTermsTravelAsGoWritesThem()
    {
        var spec = ViewSpec.Parse(
            "advisorySeverity=high%2Ccritical&reached=transitive%2Cdirect&policies=b%2Ca");
        Assert.Equal(["critical", "high"], spec.AdvisorySeverities);
        Assert.Equal(["direct", "transitive"], spec.Reached);
        Assert.Equal(["a", "b"], spec.Policies);
        Assert.Equal(
            "advisorySeverity=critical%2Chigh&policies=a%2Cb&reached=direct%2Ctransitive",
            spec.ToQueryString());
        Assert.Throws<FormatException>(() => ViewSpec.Parse("advisorySeverity=moderate"));
        Assert.Throws<FormatException>(() => ViewSpec.Parse("reached=2"));
    }

    /// <summary>
    /// A reference is matched verbatim, one parameter per value as Go writes it, because Git allows
    /// a comma in a reference. It is a different term from <c>ref</c>, the reference style.
    /// </summary>
    [Fact]
    public void AReferenceTravelsOneParameterPerValueAsGoWritesIt()
    {
        var spec = ViewSpec.Parse("at=v4&at=release%2C1&at=v4&ref=tag");
        Assert.Equal(["release,1", "v4"], spec.At);
        Assert.Equal("tag", spec.RefStyle);
        Assert.Equal("at=release%2C1&at=v4&ref=tag", spec.ToQueryString());
        Assert.Throws<FormatException>(() => ViewSpec.Parse("ref=v4"));
    }
}
