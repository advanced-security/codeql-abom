using Abom.WebAssembly.Services;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The scope is a predicate and a selection held together, and the address bar is its canonical
/// form. Every term has to survive being written and read back, or a shared link asks a narrower
/// question than the one the sender was looking at.
/// </summary>
public class ScopeServiceTests
{
    /// <summary>
    /// Every term the language has, in one address. A round trip that drops one is the defect
    /// this object exists to make impossible, and listing them individually let three of them be
    /// forgotten for as long as nobody wrote the fourth test.
    /// </summary>
    // `boundary=crossOwner` sat here until the language started refusing it: that is the canvas's
    // spelling of an edge class, and the server's boundary filter drops anything but its own three.
    private const string EveryTerm =
        "boundary=cross-owner&edges=crossOwner%2CsameOwner&flags=advisory%2Cfindings"
        + "&kinds=action%2Cworkflow&minReach=3&notProperties=team%3Aplatform&notRules=abom%2Fnoisy"
        + "&origins=seed&properties=business_id%3A3&provider=CodeQL&q=checkout&ref=tag"
        + "&repositories=octo-org%2Fbilling&rules=abom%2Funpinned&scope=owned"
        + "&selection=actions%2Fcheckout%40v4&severity=high&steps=true";

    [Fact]
    public void APredicateAndASelectionTravelAsOneText()
    {
        var scope = new ScopeService();
        scope.Set(new ViewSpec { Scope = "owned", Kinds = ["action"] });
        scope.Select(["actions/checkout@v4"]);

        var text = scope.ToQueryString();

        Assert.Contains("scope=owned", text);
        Assert.Contains("kinds=action", text);
        Assert.Contains("selection=actions%2Fcheckout%40v4", text);
    }

    [Fact]
    public void EveryTermSurvivesBeingWrittenAndReadBack()
    {
        var scope = new ScopeService();

        Assert.True(scope.ReadFrom("?" + EveryTerm));

        Assert.Null(scope.LastError);
        Assert.Equal(EveryTerm, scope.ToQueryString());
    }

    /// <summary>
    /// How the graph is drawn is not what it is showing. A scope that absorbed <c>view</c> would
    /// make a repaint look like a change of question.
    /// </summary>
    [Fact]
    public void PresentationIsNotPartOfTheScope()
    {
        var scope = new ScopeService();

        Assert.True(scope.ReadFrom("?view=priority&direction=out&from=discovery&status=queued&scope=owned"));

        Assert.Null(scope.LastError);
        Assert.Equal("scope=owned", scope.ToQueryString());
    }

    /// <summary>
    /// The unit is presentation too, and was the knob this list warns about: Inventory writes
    /// <c>unit=</c> into its own address, so every reload of a switched unit refused the address
    /// by name and started again with no scope at all. The scope simply vanished, which the term
    /// list exists to make loud rather than silent.
    /// </summary>
    [Fact]
    public void TheUnitIsHowAScopeIsDrawnRatherThanWhatItSelects()
    {
        var scope = new ScopeService();

        Assert.True(scope.ReadFrom("?unit=repositories&q=codeql"));

        Assert.Null(scope.LastError);
        Assert.Equal("q=codeql", scope.ToQueryString());
    }

    /// <summary>
    /// A predicate that quietly ignored a typo would match everything, so an unknown term is an
    /// error naming itself rather than a parameter to skip.
    /// </summary>
    [Fact]
    public void AFilterNobodyHasIsRefusedByName()
    {
        var scope = new ScopeService();

        Assert.False(scope.ReadFrom("?kindz=action"));

        Assert.NotNull(scope.LastError);
        Assert.Contains("kindz", scope.LastError);
    }

    /// <summary>
    /// Half-applying a bad address is worse than refusing it: the reader would be looking at a
    /// scope nobody asked for, with the terms that happened to parse first.
    /// </summary>
    [Fact]
    public void ARefusedAddressLeavesTheScopeAlone()
    {
        var scope = new ScopeService();
        scope.Set(new ViewSpec { Scope = "exposure" });

        Assert.False(scope.ReadFrom("?kinds=action&kindz=workflow"));

        Assert.Equal("scope=exposure", scope.ToQueryString());
    }

    /// <summary>
    /// The Explorer wrote <c>ref</c> through the shared serializer and read <c>refStyle</c> from
    /// its own parameter list, so the reference filter was dropped on every reload.
    /// </summary>
    [Fact]
    public void TheReferenceStyleUsesOneNameInBothDirections()
    {
        var scope = new ScopeService();

        Assert.True(scope.ReadFrom("?ref=tag"));

        Assert.Equal("tag", scope.Spec.RefStyle);
        Assert.Contains("ref=tag", scope.ToQueryString());
    }

    /// <summary>
    /// Absent and empty are different questions. A surface arriving with no scope shows its own
    /// defaults; one arriving with a scope shows exactly that scope, including the lists somebody
    /// deliberately emptied.
    /// </summary>
    [Fact]
    public void AnAddressWithNoScopeSaysSoRatherThanReturningAnEmptyOne()
    {
        var scope = new ScopeService();

        Assert.False(scope.ReadFrom("/explore?view=priority"));

        Assert.Null(scope.LastError);
        Assert.Equal("", scope.ToQueryString());
    }

    /// <summary>
    /// An address with no query is not a query that happens to look like a path. Reading it as one
    /// made the path itself a filter, so every plain visit was refused by name.
    /// </summary>
    [Theory]
    [InlineData("http://127.0.0.1:8099/explore")]
    [InlineData("/explore")]
    [InlineData("")]
    public void AnAddressCarryingNoQueryIsNotARefusal(string address)
    {
        var scope = new ScopeService();

        Assert.False(scope.ReadFrom(address));

        Assert.Null(scope.LastError);
    }

    [Fact]
    public void OneScopeTermIsEnoughToMakeTheAddressLiteral()
    {
        var scope = new ScopeService();

        Assert.True(scope.ReadFrom("/explore?view=priority&kinds=action"));

        Assert.Empty(scope.Spec.Flags);
    }

    /// <summary>
    /// The language cannot tell an empty list from an absent one, so a surface whose arrival
    /// default is non-empty needs the address to say which terms it actually carried. Inferring it
    /// from the parsed spec reads every partial hand-off as a deliberate emptying.
    /// </summary>
    [Fact]
    public void AnAddressSaysWhichTermsItNamed()
    {
        var scope = new ScopeService();

        Assert.True(scope.ReadFrom("?kinds=action&selection=a%2Fb&view=priority"));

        Assert.True(scope.Names("kinds"));
        Assert.True(scope.Names("selection"));
        Assert.False(scope.Names("flags"));
        Assert.False(scope.Names("view"));
    }

    /// <summary>
    /// One selection, one meaning: the same keys picked in a table and in a graph have to produce
    /// the same text, or an action over them is not the same action.
    /// </summary>
    [Fact]
    public void ASelectionIsASetWhateverOrderItWasBuiltIn()
    {
        var one = new ScopeService();
        one.Select(["b/two", "a/one", "b/two"]);

        var other = new ScopeService();
        other.Select(["a/one", "b/two"]);

        Assert.Equal(other.ToQueryString(), one.ToQueryString());
    }

    /// <summary>
    /// One parameter per key rather than a comma-joined list, for the same reason a property
    /// value gets one: a key holding the separator must not become two keys.
    /// </summary>
    [Fact]
    public void ASelectionKeyMayContainAComma()
    {
        var scope = new ScopeService();
        scope.Select(["org/repo,with-comma", "actions/checkout"]);

        var other = new ScopeService();
        Assert.True(other.ReadFrom("?" + scope.ToQueryString()));

        Assert.Equal(["actions/checkout", "org/repo,with-comma"], other.Selection);
    }

    /// <summary>
    /// Pages linking into the Explorer call the same thing <c>focus</c>, and a hand-off that lost
    /// it landed the reader on the whole estate.
    /// </summary>
    [Fact]
    public void AFocusIsReadAsASelection()
    {
        var scope = new ScopeService();

        Assert.True(scope.ReadFrom("?focus=actions%2Fcheckout"));

        Assert.Equal(["actions/checkout"], scope.Selection);
    }

    [Fact]
    public void TheScopeAnnouncesAChangeOnlyWhenItChanged()
    {
        var scope = new ScopeService();
        var announcements = 0;
        scope.Changed += () => announcements++;

        scope.Set(new ViewSpec { Scope = "owned" });
        scope.Set(new ViewSpec { Scope = "owned" });
        scope.Select([]);

        Assert.Equal(1, announcements);
    }

    /// <summary>
    /// The selection is part of the scope and no part of the predicate. Sending it to an endpoint
    /// that refuses unknown terms would fail the request the gate depends on.
    /// </summary>
    [Fact]
    public void ThePredicateHalfCarriesNoSelection()
    {
        var scope = new ScopeService();
        scope.Set(new ViewSpec { Scope = "owned" });
        scope.Select(["actions/checkout"]);

        Assert.DoesNotContain("selection", scope.Spec.ToQueryString());
        Assert.Equal("scope=owned", scope.Spec.ToQueryString());
    }
}
