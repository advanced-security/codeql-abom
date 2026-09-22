using Abom.WebAssembly.Pages;
using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Explorer's toolbar costs the width of what is asked, not of what could be.
///
/// <para>It carried a control per term for seven of the nineteen the language declares, and the
/// other twelve travelled in a saved scope with no control at all — the page said so itself:
/// <em>"Terms this page has no control for still travel."</em> Two toggle banks spent roughly 700
/// pixels stating two single choices, and neither of them was a filter.</para>
///
/// <para>Filter terms are chips from the shared picker. Presentation is not a filter and does not
/// become one: which view is drawn, how it is grouped and how far it follows are single choices,
/// so they stay single-choice controls — compact ones.</para>
/// </summary>
public sealed class ExplorerToolbarTests : AbomTestContext
{
    public ExplorerToolbarTests() => ArriveWithAScope();
    /// Every term the graph applies reaches the reader through one control, including the twelve
    /// that previously had none.
    [Fact]
    public void TheExplorerOffersTheSharedPicker()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        Assert.Single(page.FindAll("[data-abom-add-filter]"));
    }

    /// <summary>
    /// A term with a control of its own *and* a chip in the picker is two answers to one question,
    /// and a reader cannot tell which the server applied. The picker is the one that stays.
    /// </summary>
    [Fact]
    public void NoTermKeepsAControlOfItsOwnBesideThePicker()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        // The search box and the reach floor were the two filter terms with fields of their own.
        // Asserted by component rather than by CSS class, which `Hops` shares and legitimately
        // keeps: it is presentation, not a term.
        Assert.Empty(page.FindAll(".abom-filter-wide"));
        Assert.Empty(page.FindComponents<MudBlazor.MudNumericField<int?>>());
    }

    /// <summary>
    /// Scope is a filter term — <c>ViewSpec.Scope</c>, saved with every named view — so it reads
    /// as a chip like the rest rather than as a 250px bank of its own.
    /// </summary>
    [Fact]
    public void ScopeIsAFilterTermRatherThanAToggleBank()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        // Both banks go: scope becomes a chip, and the view becomes a compact choice. Asserted on
        // the component rather than on a marker attribute, which a removed control takes with it
        // and would leave this passing against anything.
        Assert.Empty(page.FindComponents<MudBlazor.MudToggleGroup<string>>());
    }

    /// <summary>
    /// The view is presentation, not a predicate: Inventory has no notion of "Neighborhood". It
    /// stays a single choice and becomes a compact one, keeping the sentence that says what each
    /// option means — that text is the only thing telling a reader what position encodes.
    /// </summary>
    /// <summary>
    /// Selecting every value of a term is the same question as selecting none, and the page must
    /// write it as none. On arrival the Explorer published all three relationship classes and
    /// <c>ref=any</c>, which reached the address as narrowing and the picker as chips claiming
    /// filters that excluded nothing.
    ///
    /// <para><c>kinds</c> is not among them: the arrival set leaves jobs and steps out, so it is
    /// a narrowing and saying so is correct.</para>
    /// </summary>
    [Fact]
    public void ATermThatSelectsEverythingIsNotWrittenAsAFilter()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();
        ArriveWithAScope("scope=owned");

        var page = Render<Explorer>();

        page.WaitForAssertion(() =>
        {
            var set = Services.GetService<ScopeService>()!.ToQueryString();
            Assert.Contains("scope=", set);
            Assert.DoesNotContain("edges=", set);
            Assert.DoesNotContain("ref=", set);
        });
    }

    /// <summary>
    /// A view that cannot be chosen says why in its own label. It carried the reason as a
    /// <c>title</c>, which MudBlazor makes unreachable: a disabled list item gets
    /// <c>pointer-events: none</c>, so nothing can be hovered and the words are written, present
    /// and invisible.
    /// </summary>
    ///
    /// <remarks>
    /// Asserted on the text the page produces rather than through the open list, because bUnit
    /// cannot open a MudSelect — which is the same reason the defect survived review: what a
    /// dropdown holds is only reachable in a browser.
    /// </remarks>
    [Fact]
    public void AViewThatNeedsASelectionSaysSoInItsLabel()
    {
        var withoutSelection = Explorer.ViewLabels(hasSelection: false);
        Assert.Contains("needs a selected node", withoutSelection["ownership"]);
        Assert.Contains("needs a selected node", withoutSelection["distance"]);
        Assert.DoesNotContain("needs a selected node", withoutSelection["neighborhood"]);

        // Once a node is named the requirement is met, and repeating it would be noise.
        Assert.DoesNotContain("needs a selected node",
            Explorer.ViewLabels(hasSelection: true)["ownership"]);
    }

    [Fact]
    public void TheViewIsACompactChoiceThatKeepsItsMeanings()
    {
        Answer("api/owned-scope", "[{\"owner\":\"octo-org\"}]");
        StubGraphModule();

        var page = Render<Explorer>();

        // The class, not a data attribute: MudSelect splats unmatched attributes onto its hidden
        // input, so `[data-abom-view-select]` matched an element no reader can see.
        Assert.Single(page.FindAll(".abom-view-select"));
        // A select renders its options only once opened, so what is checked here is that every
        // option still has a sentence to render: that text is the only thing telling a reader
        // what position encodes, and a compact control must not be a reason to drop it.
        Assert.All(Explorer.ViewMeanings,
            meaning => Assert.False(string.IsNullOrWhiteSpace(meaning)));
    }

    private void StubGraphModule()
    {
        var module = JSInterop.SetupModule("./js/graph.js");
        module.Setup<bool>("create", _ => true).SetResult(true);
        module.Setup<Explorer.LoadResult>("loadFull", _ => true)
              .SetResult(new Explorer.LoadResult(0, 0, [], false, 0));
        module.Setup<Explorer.Counts>("applyFilters", _ => true)
              .SetResult(new Explorer.Counts(12, 4, 0, 12));
        module.Setup<Explorer.Counts>("approveLargeGraph", _ => true)
              .SetResult(new Explorer.Counts(12, 4, 0, 12));
        module.Setup<Explorer.LayoutState>("applyServerLayout", _ => true)
              .SetResult(new Explorer.LayoutState(false, "Force layout", 0, 0));
        module.SetupVoid("clearServerLayout", _ => true).SetVoidResult();
        module.SetupVoid("setPalette", _ => true).SetVoidResult();
        module.SetupVoid("setAnimate", _ => true).SetVoidResult();
        module.SetupVoid("setGroupBy", _ => true).SetVoidResult();
        module.SetupVoid("setCollapsed", _ => true).SetVoidResult();
        module.SetupVoid("setView", _ => true).SetVoidResult();
        module.SetupVoid("setGeneration", _ => true).SetVoidResult();
        module.SetupVoid("setSelection", _ => true).SetVoidResult();
        module.SetupVoid("resize", _ => true).SetVoidResult();
        module.SetupVoid("rebuildNodes", _ => true).SetVoidResult();
    }
}
