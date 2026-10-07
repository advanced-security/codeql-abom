using Abom.WebAssembly.Services;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Identities shortened for a column, and the things that shortening must never do.
///
/// <para>
/// Not bUnit tests and deliberately taking no <c>BunitContext</c>: this is text in and text out,
/// and the point is that the text stays true. Every case here is a real key taken from the corpus,
/// because the failure mode is a rule that looks reasonable and quietly renames something.
/// </para>
/// </summary>
public class KeyTextTests
{
    [Fact]
    public void AWorkflowPathDropsTheDirectoryEveryWorkflowShares()
    {
        Assert.Equal("ci.yml", KeyText.WorkflowFile(".github/workflows/ci.yml"));
    }

    [Fact]
    public void APathSomewhereElseKeepsAllOfItself()
    {
        // Nothing may be dropped on the grounds that it looked like a prefix.
        Assert.Equal("ci.yml", KeyText.WorkflowFile("ci.yml"));
        Assert.Equal(".github/actions/greet/action.yml",
            KeyText.WorkflowFile(".github/actions/greet/action.yml"));
    }

    [Fact]
    public void AWorkflowReferenceSplitsIntoTheRepositoryAndTheFile()
    {
        var id = KeyText.Split("has-ghas/juice-shop/.github/workflows/ci.yml");

        Assert.Equal("has-ghas/juice-shop", id.Repository);
        Assert.Equal("ci.yml", id.Name);
    }

    // A job's key is `job:repository:path:job`; shown whole it is a prefix every row repeats.
    [Fact]
    public void AJobSplitsIntoItsRepositoryAndItsWorkflowAndName()
    {
        var id = KeyText.Split("job:octo-org/app:.github/workflows/ci.yml:build");

        Assert.Equal("octo-org/app", id.Repository);
        Assert.Equal("ci.yml › build", id.Name);
    }

    [Fact]
    public void AStepIsNamedByItsJobAndItsPlaceCountingFromOne()
    {
        var id = KeyText.Split("step:octo-org/app:.github/workflows/ci.yml:build:0");

        Assert.Equal("octo-org/app", id.Repository);
        Assert.Equal("ci.yml › build › step 1", id.Name);
    }

    [Theory]
    [InlineData("same-repository", "Same repository")]
    [InlineData("same-owner", "Same account")]
    [InlineData("cross-owner", "Another account")]
    [InlineData("", "not known")]
    public void ABoundaryIsShownInTheWordsTheVocabularyChose(string stored, string shown)
    {
        Assert.Equal(shown, KeyText.Boundary(stored));
    }

    [Fact]
    public void ACompositeActionSplitsTheSameWay()
    {
        var id = KeyText.Split(
            "advanced-security/codeql-development-toolkit/.github/actions/install-qlt");

        Assert.Equal("advanced-security/codeql-development-toolkit", id.Repository);
        Assert.Equal("install-qlt", id.Name);
    }

    [Fact]
    public void AnActionThatIsAWholeRepositoryIsNotCutInHalf()
    {
        // "actions/checkout" is the name. Taking the last segment would render it "checkout",
        // which is a different action in a different account.
        var id = KeyText.Split("actions/checkout");

        Assert.Equal("", id.Repository);
        Assert.Equal("actions/checkout", id.Name);
    }

    // Cut to one column, github/codeql-action/init, /analyze and /upload-sarif were three rows
    // reading "github/codeql-action/…".
    [Fact]
    public void AnActionInsideARepositorySplitsIntoTheRepositoryAndItsPath()
    {
        var id = KeyText.Split("github/codeql-action/upload-sarif");

        Assert.Equal("github/codeql-action", id.Repository);
        Assert.Equal("upload-sarif", id.Name);
    }

    [Fact]
    public void ADigestIsAbbreviatedRatherThanSplitOnItsSlashes()
    {
        var id = KeyText.Split(
            "docker://pandoc/core:2.14.2@sha256:"
            + "04e127c6642a2b9d447c26fe0ac6a5932efa8f508eda9f07da51b6e621dd7c19");

        Assert.Equal("", id.Repository);
        Assert.StartsWith("docker://pandoc/core:2.14.2@sha256:04e127c", id.Name);
        Assert.DoesNotContain("621dd7c19", id.Name);
        Assert.True(id.Name.Length < 50, $"still too wide for a column: {id.Name.Length}");
    }

    [Fact]
    public void ATagIsLeftAloneBecauseItIsAlreadyTheShortForm()
    {
        var id = KeyText.Split("docker://alpine:3.19");

        Assert.Equal("docker://alpine:3.19", id.Name);
    }

    [Fact]
    public void ShorteningNeverLeavesARowWithNothingToShow()
    {
        // A blank cell reads as "no value", which is a claim about the data rather than about
        // the width of a column.
        foreach (var key in new[]
                 {
                     "actions/checkout",
                     "has-ghas/juice-shop/.github/workflows/ci.yml",
                     "docker://alpine:3.19",
                     "/.github/workflows/",
                     "x",
                 })
        {
            var id = KeyText.Split(key);
            Assert.False(string.IsNullOrWhiteSpace(id.Name),
                $"{key} shortened away to nothing");
        }
    }
}
