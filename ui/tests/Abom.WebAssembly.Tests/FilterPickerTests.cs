using Abom.WebAssembly.Models;
using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// One control for every term, so a toolbar costs the width of what is set rather than the width
/// of what could be.
///
/// <para>The Explorer's toolbar exposed 7 of the 19 terms the language declares, and the other 12
/// travelled in a saved scope with no control at all — the page said so itself: <em>"Terms this
/// page has no control for still travel."</em> Adding twelve more controls is not the answer at
/// organization scale; one picker that reaches all of them is.</para>
///
/// <para>The term list comes from the server, never from a list kept here. Two declarations drift,
/// and a term this file knew about but the server had withdrawn would be a filter offered and then
/// refused by name.</para>
/// </summary>
public sealed class FilterPickerTests : AbomTestContext
{
    private const string Catalog = """
        [
          {"name":"scope","label":"Scope","help":"Which closure to ask about.","arity":"scalar",
           "values":["owned","exposure"],"surfaces":["graph","inventory"]},
          {"name":"q","label":"Name contains","help":"Free text.","arity":"scalar",
           "surfaces":["graph","inventory"]},
          {"name":"kinds","label":"Kind","help":"What a node is.","arity":"list",
           "surfaces":["graph","inventory"]},
          {"name":"rules","label":"CodeQL rule","help":"Keep what a rule reported.",
           "arity":"list","valuesFrom":"api/filters/rules/values",
           "surfaces":["graph","inventory"]},
          {"name":"properties","label":"Repository Property","help":"Keep a property value.",
           "arity":"property","valuesFrom":"api/properties","surfaces":["graph","inventory"]},
          {"name":"boundary","label":"Boundary","help":"Which side of a boundary.","arity":"scalar",
           "values":["same-repository","same-owner","cross-owner"],"surfaces":["relationships"]},
          {"name":"flags","label":"Has","help":"h","arity":"list",
           "values":["findings","advisory","unanalyzed"],"surfaces":["inventory","workflows"],
           "surfaceValues":{"workflows":["findings"]}}
        ]
        """;

    private const string Properties = """
        {"configured":"business_id","keys":["business_id","phi-scan"],
         "breakdown":{"name":"business_id","repositories":12,"withValue":12,"withoutValue":0,
           "values":[{"value":"0","repositories":8,"isDefault":true},
                     {"value":"3","repositories":4,"isDefault":false}],
           "hasDefault":true,"defaultValue":"0"}}
        """;

    public FilterPickerTests()
    {
        Answer("api/filters/rules/values", Rules);
        Answer("api/filters", Catalog);
        Fallback("api/properties", Properties);
        Fallback("api/filters/counts", """{"scope":2,"rules":3,"boundary":3}""");
    }

    private const string Rules = """
        {"values":[{"value":"actions/unpinned-tag","count":9241},
                   {"value":"abom/secrets-inherit","count":412},
                   {"value":"actions/missing-workflow-permissions","count":17}],
         "uncovered":0}
        """;

    /// <summary>
    /// How many values a term has is on the term, before it is opened. Nineteen terms is too many
    /// to open one at a time to find the four worth using, and the number is what says which.
    /// </summary>
    [Fact]
    public void ATermSaysHowManyValuesItHasBeforeItIsOpened()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());

        popover.WaitForAssertion(() => Assert.Contains(
            "3", popover.Find("[data-abom-term='rules']").TextContent));
    }

    /// <summary>
    /// A term with one value or none narrows nothing, and is ranked below the terms that do. It
    /// stays reachable: a corpus grows, and a filter that was useless this morning is not one a
    /// control should have hidden.
    /// </summary>
    [Fact]
    public void ATermThatNarrowsNothingIsRankedBelowOnesThatDo()
    {
        Answer("api/filters/counts", """{"scope":2,"rules":3,"kinds":1,"boundary":0}""");

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());

        popover.WaitForAssertion(() =>
        {
            var offered = Attributes(popover, "data-abom-term").ToList();
            Assert.True(offered.IndexOf("kinds") > offered.IndexOf("rules"),
                "a term with one value was offered above one with three");
            Assert.Contains("kinds", offered);
        });
    }

    /// <summary>
    /// A term nobody counted keeps its place. Absent is not zero: free text has no values to have
    /// none of, and ranking it down for a number it never got would bury a filter that works.
    /// </summary>
    [Fact]
    public void ATermNobodyCountedIsNotRankedDownAsThoughItHadNone()
    {
        Answer("api/filters/counts", """{"rules":3,"kinds":1}""");

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());

        popover.WaitForAssertion(() =>
        {
            var offered = Attributes(popover, "data-abom-term").ToList();
            Assert.True(offered.IndexOf("q") < offered.IndexOf("kinds"),
                "an uncounted term was ranked below one measured as narrowing nothing");
        });
    }

    /// A count nobody took is not rendered. A blank is honest; a zero is a claim.
    [Fact]
    public void ATermNobodyCountedShowsNoNumberRatherThanZero()
    {
        Answer("api/filters/counts", """{"rules":3}""");

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());

        popover.WaitForAssertion(() =>
            Assert.DoesNotContain("0", popover.Find("[data-abom-term='q']").TextContent));
    }

    /// <summary>
    /// A property is a key and a value, so it takes two choices rather than one. Offering the
    /// values of every key at once would ask a reader to recognize which key a bare `0` belongs
    /// to, and `0` is the default value of one of them.
    /// </summary>
    [Fact]
    public void APropertyTermOffersItsKeysAndThenThatKeysValues()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='properties']").Click();

        popover.WaitForAssertion(
            () => Assert.Equal(["business_id", "phi-scan"], Attributes(popover, "data-abom-key")));

        popover.WaitForElement("[data-abom-key='business_id']").Click();
        popover.WaitForAssertion(
            () => Assert.Equal(["0", "3"], Attributes(popover, "data-abom-value")));
    }

    /// <summary>
    /// A server newer than the page can offer a term the page cannot write: a tab left open across
    /// a restart runs the build it loaded. Choosing it is refused by name, and the scope stays as it
    /// was. Unrefused, the parse threw and took the whole interface down.
    /// </summary>
    [Fact]
    public void ATermThisPageCannotWriteIsRefusedByNameRatherThanBreakingThePage()
    {
        Answer("api/filters", Catalog.Replace(
            """{"name":"q",""",
            """{"name":"futureTerm","label":"Future","help":"Asked of a newer server.","arity":"list","values":["sooner","later"],"surfaces":["graph"]},{"name":"q","""));
        var before = Scope.Spec.ToQueryString();

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='futureTerm']").Click();
        popover.WaitForElement("[data-abom-value='sooner']").Click();

        page.WaitForAssertion(() =>
        {
            Assert.Contains("futureTerm", page.Find("[data-abom-picker-refusal]").TextContent);
            Assert.Equal(before, Scope.Spec.ToQueryString());
        });
    }

    /// A property is written as one key-and-value pair, and several may be asked for at once.
    [Fact]
    public void ChoosingAPropertyValueWritesThePair()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='properties']").Click();
        popover.WaitForElement("[data-abom-key='business_id']").Click();
        popover.WaitForElement("[data-abom-value='3']").Click();

        page.WaitForAssertion(() =>
        {
            var match = Assert.Single(Scope.Spec.Properties);
            Assert.Equal("business_id", match.Name);
            Assert.Equal("3", match.Value);
        });
    }

    /// <summary>
    /// A property filter applied anywhere says how many repositories carry no value for its key,
    /// because every answer beneath the chip is an answer about the rest. Said once, where the
    /// filter is, so every surface carrying a picker says it rather than each answer counting it.
    /// </summary>
    [Fact]
    public void AnAppliedPropertyFilterSaysHowManyRepositoriesCarryNoValueForItsKey()
    {
        Answer("api/properties?property=team", """
            {"configured":"business_id","keys":["business_id","team"],
             "breakdown":{"name":"team","repositories":12,"withValue":8,"withoutValue":4,
               "values":[{"value":"core","repositories":8,"isDefault":false}],
               "hasDefault":false,"defaultValue":""}}
            """);
        Scope.Set(ViewSpec.Parse("properties=team%3Acore"));

        var (_, page) = Picker();

        page.WaitForAssertion(() => Assert.Equal(
            "4 of 12 repositories carry no team, and no answer here speaks for them.",
            System.Text.RegularExpressions.Regex.Replace(
                page.Find("[data-abom-property-unvalued]").TextContent, @"\s+", " ").Trim()));
    }

    /// And a key every repository carries says nothing, or the sentence is one nobody reads.
    [Fact]
    public void AKeyEveryRepositoryCarriesSaysNothing()
    {
        Scope.Set(ViewSpec.Parse("properties=business_id%3A3"));

        var (_, page) = Picker();

        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/properties?property=business_id")));
        Assert.Empty(page.FindAll("[data-abom-property-unvalued]"));
    }

    /// <summary>
    /// The menu renders into <c>MudPopoverProvider</c>, which MainLayout supplies in the running
    /// app. Without it here the content is simply absent, which reads as the menu failing to open.
    /// </summary>
    private (IRenderedComponent<MudBlazor.MudPopoverProvider> Popover,
             IRenderedComponent<Shared.FilterPicker> Page) Picker(string surface = "graph")
    {
        var popover = Render<MudBlazor.MudPopoverProvider>();
        var page = Render<Shared.FilterPicker>(p => p.Add(x => x.Surface, surface));
        return (popover, page);
    }

    private static IReadOnlyList<string> Attributes(
        IRenderedComponent<MudBlazor.MudPopoverProvider> popover, string attribute) =>
        [.. popover.FindAll($"[{attribute}]").Select(e => e.GetAttribute(attribute) ?? "")];

    /// A term the surface would refuse is not offered. Showing it and answering "ABOM has no
    /// filter called that" turns the control into a way to discover refusals.
    [Fact]
    public void ATermTheSurfaceDoesNotApplyIsNotOffered()
    {
        var (popover, page) = Picker();

        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());

        var offered = Attributes(popover, "data-abom-term").ToList();
        Assert.Contains("scope", offered);
        Assert.DoesNotContain("boundary", offered);
    }

    /// <summary>
    /// A surface that applies no filter at all says so. "Every filter this view applies is already
    /// set" reads as a narrowing in force on the Coverage unit, whose queue nothing narrows.
    /// </summary>
    [Fact]
    public void ASurfaceThatAppliesNoFilterSaysSo()
    {
        var (popover, page) = Picker("coverage");

        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());

        popover.WaitForAssertion(() => Assert.Contains("This view applies no filters.", popover.Markup));
        Assert.DoesNotContain("already set", popover.Markup);
    }

    /// <summary>
    /// A term already in the scope that this surface does not apply is marked on its chip and named
    /// in the words a disabled named scope uses. Coverage lists the queue whole, and two chips above
    /// it reading as its narrowing claimed a filter nothing applied.
    /// </summary>
    [Fact]
    public void AChipTheSurfaceDoesNotApplySaysSo()
    {
        Scope.Set(ViewSpec.Parse("scope=exposure&flags=findings"));
        var (_, page) = Picker("coverage");

        page.WaitForAssertion(() =>
        {
            Assert.NotNull(page.Find("[data-abom-filter-chip='scope'][data-abom-chip-unapplied]"));
            Assert.NotNull(page.Find("[data-abom-filter-chip='flags'][data-abom-chip-unapplied]"));
            Assert.Equal("Not applied here", page.Find("[data-abom-unapplied]").TextContent.Trim());
        });
    }

    /// A term the surface applies is not marked, and nothing is said.
    [Fact]
    public void AChipTheSurfaceAppliesIsNotMarked()
    {
        Scope.Set(ViewSpec.Parse("scope=exposure"));
        var (_, page) = Picker("inventory");

        page.WaitForAssertion(() =>
        {
            Assert.Empty(page.FindAll("[data-abom-chip-unapplied]"));
            Assert.Empty(page.FindAll("[data-abom-unapplied]"));
            Assert.NotNull(page.Find("[data-abom-filter-chip='scope']"));
        });
    }

    /// The whole point of the control: nothing selected costs no width beyond the one button that
    /// adds something.
    [Fact]
    public void NothingSelectedRendersNoChips()
    {
        var (_, page) = Picker();

        page.WaitForAssertion(() => Assert.Empty(page.FindAll("[data-abom-filter-chip]")));
    }

    /// At nineteen terms a list is a wall, and at organization scale the values behind one are
    /// worse. Typing narrows it.
    [Fact]
    public void TheTermListNarrowsAsYouType()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());

        popover.Find("[data-abom-term-search] input").Input("kind");

        Assert.Equal(["kinds"], Attributes(popover, "data-abom-term"));
    }

    /// A term already set is not offered again: a second Scope chip would be two answers to one
    /// question, and the reader could not tell which the server applied.
    [Fact]
    public void ATermAlreadySetIsNotOfferedTwice()
    {
        Scope.Set(new ViewSpec { Scope = "owned" });

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());

        Assert.DoesNotContain("scope", Attributes(popover, "data-abom-term"));
    }

    /// A set term reads as its label and value rather than as a query-string key, because
    /// `notProperties=business_id:0` is not a sentence anybody wrote.
    [Fact]
    public void ASetTermRendersAsAChipNamingItsLabelAndValue()
    {
        Scope.Set(new ViewSpec { Scope = "owned" });

        var (_, page) = Picker();

        page.WaitForAssertion(() =>
        {
            var chip = page.Find("[data-abom-filter-chip='scope']");
            Assert.Contains("Scope", chip.TextContent);
            Assert.Contains("owned", chip.TextContent);
        });
    }

    /// The picker owns no scope state. Everything goes through ScopeService, or the address and
    /// the screen come to disagree about what is being asked.
    [Fact]
    public void RemovingAChipClearsTheTermFromTheSharedScope()
    {
        Scope.Set(new ViewSpec { Scope = "owned", Search = "checkout" });

        var (_, page) = Picker();
        page.WaitForAssertion(
            () => page.Find("[data-abom-filter-chip='scope'] [data-abom-chip-remove]").Click());

        Assert.Equal("", Scope.Spec.Scope);
        Assert.Equal("checkout", Scope.Spec.Search);
    }

    /// <summary>
    /// A value list built from rows that recorded a value describes only those rows. `provider`
    /// offers one value over 884 of 11,806 findings, the rest predating the column — so choosing
    /// the only provider on offer answers for a fourteenth of the corpus, confidently and without
    /// saying so. The same rule the property breakdown already follows: name what is left out.
    /// </summary>
    [Fact]
    public void AValueListSaysHowMuchOfThePopulationItCannotSpeakFor()
    {
        Answer("api/filters/rules/values", """
            {"values":[{"value":"actions/unpinned-tag","count":884}],"uncovered":10922}
            """);

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='rules']").Click();

        popover.WaitForAssertion(() =>
            Assert.Contains("10,922", popover.Find("[data-abom-value-coverage]").TextContent));
    }

    /// A term every row carries leaves nothing out, and says nothing rather than "0 left out".
    [Fact]
    public void ATermThatCoversEveryRowSaysNothingRatherThanNoneLeftOut()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='rules']").Click();
        popover.WaitForElement("[data-abom-value]");

        Assert.Empty(popover.FindAll("[data-abom-value-coverage]"));
    }

    /// <summary>
    /// A term whose values come from the corpus shows them on arrival. Asking a reader to type
    /// first is a memory test: <c>actions/missing-workflow-permissions</c> is not a name anybody
    /// recalls, and an empty box gives no way to discover that twenty-one rules reported at all.
    /// </summary>
    [Fact]
    public void ACorpusDerivedTermShowsItsValuesBeforeAnythingIsTyped()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='rules']").Click();

        popover.WaitForAssertion(() => Assert.Equal(
            ["actions/unpinned-tag", "abom/secrets-inherit", "actions/missing-workflow-permissions"],
            Attributes(popover, "data-abom-value")));
    }

    /// <summary>
    /// Typing narrows what is shown rather than deciding what is sent. The box was the only way
    /// in; it is a way to find one of many now, and a list that ignored it would be unusable at
    /// the size these lists reach.
    /// </summary>
    [Fact]
    public void TypingNarrowsTheValuesRatherThanBeingTheOnlyWayIn()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='rules']").Click();
        popover.WaitForElement("[data-abom-value-search] input").Input("unpinned");

        popover.WaitForAssertion(() =>
            Assert.Equal(["actions/unpinned-tag"], Attributes(popover, "data-abom-value")));
    }

    /// <summary>
    /// How much of the corpus a value accounts for is on the value. Twenty-one rules where two
    /// are nine tenths of the findings is a list in which alphabetical order buries the answer,
    /// and a count is the only thing that says which one is worth opening.
    /// </summary>
    [Fact]
    public void AValueSaysHowMuchOfTheCorpusItAccountsFor()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='rules']").Click();

        popover.WaitForAssertion(() => Assert.Contains(
            "9,241", popover.Find("[data-abom-value='actions/unpinned-tag']").TextContent));
    }

    /// <summary>
    /// A value the corpus has not produced can still be filtered by. A rule that exists and has
    /// reported nothing yet is absent from the list, and a control that only offered the list
    /// would make a scope naming it unbuildable here — the server accepts it either way.
    /// </summary>
    [Fact]
    public void AValueTheCorpusHasNotProducedCanStillBeTyped()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='rules']").Click();
        popover.WaitForElement("[data-abom-value-search] input").Input("abom/not-yet-reported");

        popover.WaitForElement("[data-abom-value-apply]").Click();

        page.WaitForAssertion(() =>
            Assert.Contains("rules=abom%2Fnot-yet-reported", Scope.ToQueryString()));
    }

    /// <summary>
    /// The way to apply typed text appears only when the text is not already on the list. Sitting
    /// there disabled above every list it is a control that does nothing, and the reader scanning
    /// past it has to work out why — twice, because it is between the box and what they came for.
    /// </summary>
    [Fact]
    public void ApplyingTypedTextIsOfferedOnlyWhenTheListDoesNotAlreadyHaveIt()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='rules']").Click();
        popover.WaitForElement("[data-abom-value]");

        Assert.Empty(popover.FindAll("[data-abom-value-apply]"));

        // Narrowing to something the list holds is still choosing from the list.
        popover.Find("[data-abom-value-search] input").Input("unpinned");
        popover.WaitForAssertion(() => Assert.Empty(popover.FindAll("[data-abom-value-apply]")));

        popover.Find("[data-abom-value-search] input").Input("abom/not-yet-reported");
        popover.WaitForAssertion(() => Assert.NotNull(popover.Find("[data-abom-value-apply]")));
    }

    /// A term with no list is typed, so the way to apply what was typed has to be there at once.
    [Fact]
    public void ATermWithNoListOffersTheWayToApplyWhatIsTyped()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='q']").Click();
        popover.WaitForElement("[data-abom-value-search] input").Input("checkout");

        popover.WaitForAssertion(() => Assert.NotNull(popover.Find("[data-abom-value-apply]")));
    }

    /// A term with a closed vocabulary offers it rather than asking a reader to type one of three
    /// words exactly.
    [Fact]
    public void ATermWithAClosedVocabularyOffersItsValues()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='scope']").Click();

        Assert.Equal(["owned", "exposure"], Attributes(popover, "data-abom-value"));
    }

    /// Choosing a value writes it to the shared scope, which is the only thing the picker does.
    [Fact]
    public void ChoosingAValueWritesItToTheSharedScope()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='scope']").Click();
        popover.Find("[data-abom-value='exposure']").Click();

        Assert.Equal("exposure", Scope.Spec.Scope);
    }

    /// <summary>
    /// A surface answering some of a term's values offers only those. The workflow unit narrows by
    /// findings and refuses the other two flags by name, so offering them is a refusal waiting for
    /// a click.
    /// </summary>
    [Fact]
    public void ASurfaceThatAnswersSomeValuesOffersOnlyThose()
    {
        var (popover, page) = Picker("workflows");
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='flags']").Click();

        Assert.Equal(["findings"], Attributes(popover, "data-abom-value"));
    }

    /// A term whose values come from the corpus has no closed list to offer, so it is typed. The
    /// control must not present an empty list as "there are none".
    [Fact]
    public void ATermWithNoClosedVocabularyIsTypedRatherThanShownAsEmpty()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='q']").Click();

        Assert.Empty(popover.FindAll("[data-abom-value]"));
        Assert.NotNull(popover.Find("[data-abom-value-entry] input"));
    }

    /// <summary>
    /// A property key most of the organization does not carry narrows to a fraction of it, and
    /// the fraction is the point: choosing a value there is choosing a subset of the 8 repositories
    /// that answered the question, not of the 25 that exist. A control that showed only the values
    /// would let a reader take a filtered count for an organization-wide one.
    /// </summary>
    [Fact]
    public void AKeyMostOfTheOrganizationDoesNotCarrySaysHowManyItLeavesOut()
    {
        Answer("api/properties?property=phi-scan", """
            {"configured":"business_id","keys":["business_id","phi-scan"],
             "breakdown":{"name":"phi-scan","repositories":25,"withValue":8,"withoutValue":17,
               "values":[{"value":"pending","repositories":8}],
               "hasDefault":false,"defaultValue":""}}
            """);

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='properties']").Click();
        popover.WaitForElement("[data-abom-key='phi-scan']").Click();

        popover.WaitForAssertion(() =>
        {
            var said = popover.Find("[data-abom-property-coverage]").TextContent;
            Assert.Contains("17", said);
            Assert.Contains("25", said);
        });
    }

    /// <summary>
    /// A required property with a declared default puts every repository that never answered into
    /// one bucket. That bucket is the largest group and nobody chose it, so it is marked rather
    /// than left to read as a decision 8 repositories made.
    /// </summary>
    [Fact]
    public void TheDefaultBucketIsMarkedAsOneNobodyChose()
    {
        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='properties']").Click();
        popover.WaitForElement("[data-abom-key='business_id']").Click();

        popover.WaitForAssertion(() =>
        {
            Assert.Contains("organization default",
                popover.Find("[data-abom-value='0']").TextContent);
            Assert.DoesNotContain("organization default",
                popover.Find("[data-abom-value='3']").TextContent);
        });
    }

    /// <summary>
    /// A key the organization administers but has set on nothing has no values to offer. An empty
    /// list reads as a control that failed; what is true is that the key exists and is unused.
    /// </summary>
    [Fact]
    public void AKeyTheOrganizationSetsNowhereSaysSoRatherThanShowingNothing()
    {
        Answer("api/properties?property=phi-scan", """
            {"configured":"business_id","keys":["business_id","phi-scan"],
             "breakdown":{"name":"phi-scan","repositories":25,"withValue":0,"withoutValue":25,
               "values":[],"hasDefault":false,"defaultValue":""}}
            """);

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='properties']").Click();
        popover.WaitForElement("[data-abom-key='phi-scan']").Click();

        popover.WaitForAssertion(() => Assert.NotEmpty(
            popover.Find("[data-abom-property-empty]").TextContent.Trim()));
        Assert.Empty(popover.FindAll("[data-abom-value]"));
    }

    /// <summary>
    /// An organization that administers no custom properties at all is a different thing from one
    /// whose properties this control failed to read, and the term must say which.
    /// </summary>
    [Fact]
    public void AnOrganizationWithNoPropertiesAtAllSaysThatInstead()
    {
        Answer("api/properties", """
            {"configured":"","keys":[],
             "breakdown":{"name":"","repositories":0,"withValue":0,"withoutValue":0,
               "values":[],"hasDefault":false,"defaultValue":""}}
            """);

        var (popover, page) = Picker();
        page.WaitForAssertion(() => page.Find("[data-abom-add-filter]").Click());
        popover.WaitForElement("[data-abom-term='properties']").Click();

        popover.WaitForAssertion(() => Assert.NotEmpty(
            popover.Find("[data-abom-property-empty]").TextContent.Trim()));
        Assert.Empty(popover.FindAll("[data-abom-key]"));
    }

    /// <summary>
    /// A page that narrows the view itself writes the shared scope after this control has already
    /// rendered. Waiting to be re-rendered by a parent left the Explorer drawing 205 nodes of
    /// 57,844 under a picker showing no filters at all: the narrowing was real, the chips said
    /// there wasn't any, and the only reading available was that nothing had been applied.
    /// </summary>
    [Fact]
    public void AScopeSetAfterThisControlRenderedStillReachesTheChips()
    {
        var (_, page) = Picker();
        Assert.Empty(page.FindAll("[data-abom-filter-chip]"));

        Scope.Set(ViewSpec.Parse("scope=exposure"));

        page.WaitForAssertion(() =>
            Assert.NotNull(page.Find("[data-abom-filter-chip='scope']")));
    }

    /// <summary>
    /// A list term holding many values cannot be read as a list. Seven kinds spelled out ran the
    /// Explorer's bar to 213 pixels and past the width of the chip row, where a reader scanning
    /// for what is applied sees a wall rather than a filter. The count is what is legible at a
    /// glance; the values themselves stay reachable as the chip's title.
    /// </summary>
    [Fact]
    public void AChipHoldingManyValuesCountsThemRatherThanListingThem()
    {
        Scope.Set(ViewSpec.Parse("kinds=action,repo,workflow,org"));

        var (_, page) = Picker();

        page.WaitForAssertion(() =>
        {
            var chip = page.Find("[data-abom-filter-chip='kinds']");
            Assert.Contains("4", chip.TextContent);
            Assert.DoesNotContain("workflow", chip.TextContent);
            Assert.Contains("workflow", chip.GetAttribute("title") ?? string.Empty);
        });
    }

    /// A few values are still read as values: a count would hide what is shorter than the count.
    [Fact]
    public void AChipHoldingAFewValuesStillNamesThem()
    {
        Scope.Set(ViewSpec.Parse("kinds=action,repo"));

        var (_, page) = Picker();

        page.WaitForAssertion(() =>
            Assert.Contains("action", page.Find("[data-abom-filter-chip='kinds']").TextContent));
    }

    private ScopeService Scope => Services.GetService<ScopeService>()!;
}
