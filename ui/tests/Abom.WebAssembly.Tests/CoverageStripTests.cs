using Abom.WebAssembly.Models;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The strip is on every page, so whatever it calls a number is what the whole interface means by
/// it. It used to print the corpus figure under the words "repositories in scope", which reads as
/// a claim of responsibility for repositories nobody declared.
/// </summary>
public class CoverageStripTests : AbomTestContext
{
    private static Summary Corpus(int seed, int owned) => new()
    {
        SeedRepos = seed,
        OwnedRepos = owned,
        UpstreamRepos = 895,
        Findings = 11370,
        Rules = 21,
        LinkedFindings = 11370,
        Advisories = 352,
    };

    /// <summary>
    /// The reference corpus's own state: 204 seeded repositories and nothing declared. Calling
    /// those 204 "in scope" tells an operator they are responsible for a corpus they never
    /// claimed.
    /// </summary>
    [Fact]
    public void TheCorpusFigureIsNotPresentedAsWhatTheOperatorOwns()
    {
        var cut = Render<Shared.CoverageStrip>(p => p.Add(s => s.Summary, Corpus(204, 0)));

        Assert.DoesNotContain("in scope", cut.Markup);
        Assert.DoesNotContain("under review", cut.Markup);
    }

    /// <summary>
    /// Both halves, always. Hiding the corpus figure would make coverage unreadable, which is the
    /// same mistake in the other direction.
    /// </summary>
    [Fact]
    public void TheCorpusFigureIsStillStatedBesideWhatIsOwned()
    {
        var cut = Render<Shared.CoverageStrip>(p => p.Add(s => s.Summary, Corpus(204, 12)));

        Assert.Contains("204", cut.Markup);
        Assert.Contains("12", cut.Markup);
    }

    /// <summary>
    /// A declaration nobody made is not a declaration of zero repositories. The strip says so
    /// rather than printing a nought, which would read as a measured emptiness.
    /// </summary>
    [Fact]
    public void NothingDeclaredIsSaidInWordsRatherThanAsAZero()
    {
        var cut = Render<Shared.CoverageStrip>(p => p.Add(s => s.Summary, Corpus(204, 0)));

        var owned = cut.Find("[data-abom-owned-count]");
        Assert.DoesNotContain("0 ", owned.TextContent);
        Assert.Contains("declared", owned.TextContent, System.StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// And when something is owned, the count leads, because every other figure on every page is
    /// scoped by it.
    /// </summary>
    [Fact]
    public void WhatIsOwnedIsStatedAsACountWhenThereIsOneToState()
    {
        var cut = Render<Shared.CoverageStrip>(p => p.Add(s => s.Summary, Corpus(204, 198)));

        var owned = cut.Find("[data-abom-owned-count]");
        Assert.Contains("198", owned.TextContent);
        Assert.Contains("yours", owned.TextContent, System.StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// The cell counts declarations and sits in a row of measurements — beside "upstream analyzed"
    /// it reads as a claim about what was examined, and on the working corpus those are very
    /// different: 25 of 25 declared, 17 of them ever analyzed.
    ///
    /// <para>So the label has to carry the word. A seventh cell stating the never-analyzable
    /// remainder belongs to <c>plans/ui/theme.md</c>, whose grid is tuned for six.</para>
    /// </summary>
    [Fact]
    public void TheOwnedFigureSaysItIsADeclarationRatherThanAMeasurement()
    {
        var cut = Render<Shared.CoverageStrip>(p => p.Add(s => s.Summary, Corpus(204, 198)));

        var owned = cut.Find("[data-abom-owned-count]");
        Assert.Contains("declared", owned.TextContent, System.StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// The strip is a grid whose column counts are chosen so the last row is never one orphaned
    /// item: 2, 3 and 6 all divide six. Seven items cannot avoid an orphan at any of them, and a
    /// seventh was added and measured doing exactly that before this test existed.
    ///
    /// <para>bUnit has no CSS engine and cannot see the wrap. It can count the cells the wrap is
    /// computed from, which is the part that actually changes when someone adds a figure.</para>
    /// </summary>
    [Fact]
    public void TheStripStaysWithinTheSixCellsItsGridIsTunedFor()
    {
        var full = Corpus(204, 198) with
        {
            Frontier = new Dictionary<string, int> { ["pending"] = 813 },
            LinkedFindings = 10858,
        };

        var cut = Render<Shared.CoverageStrip>(p => p.Add(s => s.Summary, full));

        var cells = cut.FindAll(".abom-coverage-item");
        Assert.Equal(6, cells.Count);
    }

    /// <summary>
    /// Both conditional cells are absent on a corpus with nothing outstanding, which is the case
    /// the grid's smaller column counts have to divide.
    /// </summary>
    [Fact]
    public void ACorpusWithNothingOutstandingDropsTheTwoCaveatCells()
    {
        var cut = Render<Shared.CoverageStrip>(p => p.Add(s => s.Summary, Corpus(204, 198)));

        Assert.Equal(4, cut.FindAll(".abom-coverage-item").Count);
    }
}
