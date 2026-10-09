using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The form a request is written in: what it is for, a name and a lasting id, as many examples on
/// each side as the requester has, where to read about the weakness, and the metadata a new query
/// should declare.
/// </summary>
public class QueryRequestFormTests : AbomTestContext
{
    private const string SignedIn = """{"authenticated":true,"login":"operator","accounts":[]}""";

    private const string None = """
        {"requests":[],"kinds":["","missed","false-alarm","both"],
         "statuses":["open","in-progress","done","declined"],
         "precisions":["","low","medium","high","very-high"],
         "severities":["","error","warning","recommendation"]}
        """;

    private const string Catalog = """
        {"queries":[
          {"queryId":"acme/a","queryVersion":"v2","source":"custom","working":true,"steps":[],
           "metadata":{"precision":"high","problem.severity":"warning","tags":"security external/cwe/cwe-829"}},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","source":"upstream","steps":[]}
        ],"stages":["dev","test"]}
        """;

    private const string Read = """
        {"side":"non-compliant","text":"jobs:\n  a: {}\n",
         "url":"https://github.com/octo/app/blob/main/.github/workflows/ci.yml#L2-L3",
         "repository":"octo/app","path":".github/workflows/ci.yml","ref":"main",
         "commit":"1111111111111111111111111111111111111111","startLine":2,"endLine":3}
        """;

    private IRenderedComponent<Shared.QueryRequests> Open()
    {
        Answer("api/query-requests", None);
        Answer("api/accounts", SignedIn);
        Answer("api/queries", Catalog);
        var cut = Render<Shared.QueryRequests>();
        cut.WaitForAssertion(() => cut.Find("[data-abom-request-new]"));
        cut.Find("[data-abom-request-new]").Click();
        cut.WaitForAssertion(() => cut.Find("[data-abom-request-form]"));
        return cut;
    }

    private static void Type(IRenderedComponent<Shared.QueryRequests> cut, string selector, string text) =>
        cut.Find(selector).Input(text);

    private static void Paste(IRenderedComponent<Shared.QueryRequests> cut, string side, string text)
    {
        cut.Find($"[data-abom-request-{side}] [data-abom-example-add]").Click();
        cut.FindAll($"[data-abom-request-{side}] textarea")[^1].Input(text);
    }

    private static void FillTheMinimum(IRenderedComponent<Shared.QueryRequests> cut)
    {
        Type(cut, "[data-abom-request-title] input", "Self-hosted runner on a PR");
        Type(cut, "[data-abom-request-description] textarea", "flag a self-hosted runner");
        Paste(cut, "compliant", "runs-on: ubuntu-latest");
        Paste(cut, "noncompliant", "runs-on: self-hosted");
    }

    private string SavedBody(IRenderedComponent<Shared.QueryRequests> cut)
    {
        cut.Find("[data-abom-request-save]").Click();
        string body = "";
        cut.WaitForAssertion(() =>
            body = Assert.Single(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/query-requests")).Body);
        return body;
    }

    [Fact]
    public void TheFormIsNamedForWhatItAsksAndSaysWhatARequestIsFor()
    {
        var cut = Open();

        Assert.Equal("New CodeQL Query Request", cut.Find("[data-abom-request-heading]").TextContent.Trim());
        var standfirst = cut.Find("[data-abom-request-standfirst]").TextContent;
        Assert.Contains("new or improved CodeQL query", standfirst);
        Assert.Contains("GitHub Actions", standfirst);
    }

    [Fact]
    public void ARequestCannotBeSavedWithoutATitle()
    {
        var cut = Open();
        FillTheMinimum(cut);
        Type(cut, "[data-abom-request-title] input", "");

        Assert.True(cut.Find("[data-abom-request-save]").HasAttribute("disabled"));
    }

    /// <summary>The id is written from the title until somebody writes it, and then it is theirs.</summary>
    [Fact]
    public void TheIdFollowsTheTitleUntilItIsWritten()
    {
        var cut = Open();

        Type(cut, "[data-abom-request-title] input", "Self-hosted runner on a PR!");
        Assert.Equal("self-hosted-runner-on-a-pr", cut.Find("[data-abom-request-slug] input").GetAttribute("value"));

        FillTheMinimum(cut);
        Type(cut, "[data-abom-request-slug] input", "pr-runner");
        Type(cut, "[data-abom-request-title] input", "Something else");
        var body = SavedBody(cut);
        Assert.Contains("\"title\":\"Something else\",\"slug\":\"pr-runner\"", body);
    }

    /// <summary>An id written from a long title stops where a word ends, never inside one.</summary>
    [Fact]
    public void AnIdFromALongTitleEndsOnAWholeWord()
    {
        Assert.Equal("flag-a-reusable-workflow-called-with-secrets-inherit-from-a",
            Shared.QueryRequestForm.Slugify(
                "Flag a reusable workflow called with secrets inherit from a pulls request target"));
        Assert.Equal("flag-a-reusable-workflow-called-with-secrets-inherit-from-a-pull",
            Shared.QueryRequestForm.Slugify(
                "Flag a reusable workflow called with secrets inherit from a pull request target"));
    }

    /// <summary>
    /// The query id is typed or picked, and the form says before saving what naming it asks for: an
    /// id the operator's pack holds is improved, an id nothing holds is a new query, and an
    /// upstream one is not the operator's to change.
    /// </summary>
    [Theory]
    [InlineData("", "agent")]
    [InlineData("acme/a", "improve")]
    [InlineData("acme/self-hosted-pr-runner", "create")]
    [InlineData("actions/unpinned-tag", "not-in-pack")]
    [InlineData("Not An Id", "invalid")]
    public void AQueryIdSaysWhetherTheRequestImprovesOrCreates(string id, string target)
    {
        var cut = Open();

        Type(cut, "[data-abom-request-query] input", id);

        Assert.Equal(target, cut.Find("[data-abom-request-target]").GetAttribute("data-abom-request-target"));
    }

    /// <summary>
    /// Whether an example is pasted or linked is the requester's first choice on each side, and a
    /// choice can be taken back.
    /// </summary>
    [Fact]
    public void ANewRequestLeavesHowEachExampleIsGivenToTheRequester()
    {
        var cut = Open();

        foreach (var side in new[] { "compliant", "noncompliant" })
        {
            Assert.Empty(cut.FindAll($"[data-abom-request-{side}] [data-abom-request-example]"));
            cut.Find($"[data-abom-request-{side}] [data-abom-example-add]");
            cut.Find($"[data-abom-request-{side}] [data-abom-example-add-link]");
        }

        cut.Find("[data-abom-request-compliant] [data-abom-example-add-link]").Click();
        cut.Find("[data-abom-request-compliant] [data-abom-example-remove]").Click();

        Assert.Empty(cut.FindAll("[data-abom-request-compliant] [data-abom-request-example]"));
    }

    [Fact]
    public void EveryExampleOnEitherSideIsSentInOrder()
    {
        var cut = Open();
        FillTheMinimum(cut);

        Paste(cut, "compliant", "runs-on: [self-hosted, acme]");
        var body = SavedBody(cut);

        Assert.Contains("\"examples\":[" +
            "{\"side\":\"compliant\",\"text\":\"runs-on: ubuntu-latest\"" , body);
        var second = body.IndexOf("runs-on: [self-hosted, acme]", StringComparison.Ordinal);
        var third = body.IndexOf("\"side\":\"non-compliant\"", StringComparison.Ordinal);
        Assert.True(second > 0 && third > second, body);
    }

    [Fact]
    public void ARemovedExampleIsNotSent()
    {
        var cut = Open();
        FillTheMinimum(cut);
        Paste(cut, "compliant", "runs-on: dropped");

        cut.FindAll("[data-abom-request-compliant] [data-abom-example-remove]")[1].Click();

        Assert.DoesNotContain("dropped", SavedBody(cut));
    }

    /// <summary>
    /// A link is read by the server, which says which commit and lines it read; that is what the
    /// form shows and what is saved, so the test built from it cannot move with the branch.
    /// </summary>
    [Fact]
    public void ALinkedExampleShowsTheCommitAndLinesItWasReadAt()
    {
        AnswerWrite("api/query-requests/example", Read);
        var cut = Open();
        FillTheMinimum(cut);

        cut.Find("[data-abom-request-noncompliant] [data-abom-example-add-link]").Click();
        Type(cut, "[data-abom-request-noncompliant] [data-abom-example-url] input",
            "https://github.com/octo/app/blob/main/.github/workflows/ci.yml#L2-L3");
        cut.Find("[data-abom-request-noncompliant] [data-abom-example-read]").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains("octo/app/.github/workflows/ci.yml@1111111 L2–L3",
                cut.Find("[data-abom-example-pinned]").TextContent));
        Assert.Contains("\"commit\":\"1111111111111111111111111111111111111111\"", SavedBody(cut));
    }

    [Fact]
    public void ALinkTheServerCannotReadSaysWhy()
    {
        RefuseWrite("api/query-requests/example", System.Net.HttpStatusCode.BadRequest,
            "GitHub holds no file at ci.yml in 1111111");
        var cut = Open();

        cut.Find("[data-abom-request-noncompliant] [data-abom-example-add-link]").Click();
        Type(cut, "[data-abom-request-noncompliant] [data-abom-example-url] input",
            "https://github.com/octo/app/blob/main/ci.yml#L1");
        cut.Find("[data-abom-request-noncompliant] [data-abom-example-read]").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains("GitHub holds no file", cut.Find("[data-abom-example-refused]").TextContent));
    }

    /// <summary>A CVE or a CWE says, before saving, the tag it gives the query's findings.</summary>
    [Fact]
    public void ReferencesShowTheTagsTheyAdd()
    {
        var cut = Open();
        FillTheMinimum(cut);

        Type(cut, "[data-abom-request-references] input", "cve-2025-30066, CWE-829 https://example.com/advisory");
        cut.Find("[data-abom-reference-add]").Click();

        Assert.Equal(3, cut.FindAll("[data-abom-reference]").Count);
        var implied = cut.FindAll("[data-abom-reference-tag]").Select(t => t.TextContent.Trim()).ToList();
        Assert.Equal(["external/cve/cve-2025-30066", "external/cwe/cwe-829"], implied);
        Assert.Contains("\"value\":\"CWE-829\"", SavedBody(cut));
    }

    [Theory]
    [InlineData("CWE-79", "external/cwe/cwe-079")]
    [InlineData("cwe-0829", "external/cwe/cwe-829")]
    [InlineData("CVE-2025-30066", "external/cve/cve-2025-30066")]
    [InlineData("GHSA-mrrh-fwg8-r2c3", null)]
    public void AReferenceImpliesTheTagCodeQLsStyleGivesIt(string reference, string? tag) =>
        Assert.Equal(tag, Shared.QueryRequestForm.ImpliedTag(reference));

    [Fact]
    public void ANewQueryProposesTheMetadataItShouldDeclare()
    {
        var cut = Open();
        FillTheMinimum(cut);
        Type(cut, "[data-abom-request-query] input", "acme/self-hosted-pr-runner");

        cut.Find("[data-abom-request-metadata-toggle]").Click();
        Type(cut, "[data-abom-metadata-description] textarea", "A self-hosted runner reached from a fork.");
        Type(cut, "[data-abom-metadata-tags] input", "security actions");
        cut.Find("[data-abom-metadata-tag-add]").Click();
        Type(cut, "[data-abom-metadata-security-severity] input", "8.8");
        var body = SavedBody(cut);

        Assert.Contains("\"description\":\"A self-hosted runner reached from a fork.\"", body);
        Assert.Contains("\"tags\":[\"security\",\"actions\"]", body);
        Assert.Contains("\"securitySeverity\":8.8", body);
    }

    /// <summary>A query declares a tag once, however often it was typed.</summary>
    [Fact]
    public void ATagTypedTwiceIsDeclaredOnce()
    {
        var cut = Open();
        FillTheMinimum(cut);
        cut.Find("[data-abom-request-metadata-toggle]").Click();
        Type(cut, "[data-abom-metadata-tags] input", "actions security actions");
        cut.Find("[data-abom-metadata-tag-add]").Click();
        Type(cut, "[data-abom-metadata-tags] input", "security");
        cut.Find("[data-abom-metadata-tag-add]").Click();

        Assert.Equal(["actions", "security"],
            cut.FindAll("[data-abom-metadata-tag]").Select(c => c.TextContent.Trim()));
    }

    /// <summary>
    /// Code scanning reads @security-severity only on a query tagged security, so proposing one tags
    /// the query security where the requester can see it, and taking that tag away says why it is
    /// needed rather than leaving the server to refuse the save.
    /// </summary>
    [Fact]
    public void ASecuritySeverityTagsTheQuerySecurity()
    {
        var cut = Open();
        FillTheMinimum(cut);
        cut.Find("[data-abom-request-metadata-toggle]").Click();
        Type(cut, "[data-abom-metadata-tags] input", "third-party-api-audit");
        cut.Find("[data-abom-metadata-tag-add]").Click();

        Type(cut, "[data-abom-metadata-security-severity] input", "3.0");

        Assert.Equal(["security", "third-party-api-audit"],
            cut.FindAll("[data-abom-metadata-tag]").Select(c => c.TextContent.Trim()));
        cut.Find("[data-abom-metadata-tag] .mud-chip-close-button").Click();
        Assert.True(cut.Find("[data-abom-request-save]").HasAttribute("disabled"));
        Assert.Contains("security tag", cut.Find("[data-abom-metadata-security-severity]").TextContent);
    }

    /// <summary>
    /// Improving a query keeps the metadata it declares: the form shows the catalog's and proposes
    /// none, which is what the server would refuse otherwise.
    /// </summary>
    [Fact]
    public void ImprovingAQueryShowsItsMetadataAndProposesNone()
    {
        var cut = Open();
        FillTheMinimum(cut);
        Type(cut, "[data-abom-request-query] input", "acme/a");

        cut.Find("[data-abom-request-metadata-toggle]").Click();
        var declared = cut.Find("[data-abom-metadata-declared]").TextContent;
        Assert.Contains("external/cwe/cwe-829", declared);
        Assert.Empty(cut.FindAll("[data-abom-metadata-description] textarea"));
        var body = SavedBody(cut);

        Assert.Contains("\"metadata\":{\"description\":\"\",\"precision\":\"\",\"problemSeverity\":\"\"," +
            "\"securitySeverity\":null,\"tags\":[]}", body);
    }
}
