using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>A request names what to catch and shows code on both sides; both are required.</summary>
public class QueryRequestsTests : AbomTestContext
{
    private const string SignedIn = """{"authenticated":true,"login":"operator","accounts":[]}""";
    private const string SignedOut = """{"authenticated":false,"login":"","accounts":[]}""";

    private const string None = """
        {"requests":[],"kinds":["","missed","false-alarm","both"],
         "statuses":["open","in-progress","done","declined"]}
        """;

    private const string One = """
        {"requests":[{"rowId":7,"description":"flag a self-hosted runner\nreached from pull_request",
          "compliant":"runs-on: ubuntu-latest","nonCompliant":"runs-on: self-hosted",
          "queryId":"","kind":"missed","status":"open","requestedBy":"operator",
          "createdAt":"2026-09-22T16:00:00Z","updatedAt":"2026-09-22T16:00:00Z"}],
         "kinds":["","missed","false-alarm","both"],"statuses":["open","in-progress","done","declined"]}
        """;

    private IRenderedComponent<Shared.QueryRequests> Render(string list, string identity = SignedIn)
    {
        Answer("api/query-requests", list);
        Answer("api/accounts", identity);
        var cut = Render<Shared.QueryRequests>();
        cut.WaitForAssertion(() => cut.Find("[data-abom-requests]"));
        return cut;
    }

    private static void Write(IRenderedComponent<Shared.QueryRequests> cut, string field, string text) =>
        cut.Find($"[data-abom-request-{field}] textarea").Input(text);

    [Fact]
    public void NoRequestsSaysWhatARequestIsFor()
    {
        var cut = Render(None);

        Assert.Contains("COMPLIANT", cut.Find("[data-abom-requests-empty]").TextContent);
    }

    [Fact]
    public void TheExamplesAreLabeledTheWayAQueryTestMarksThem()
    {
        var cut = Render(None);
        cut.Find("[data-abom-request-new]").Click();

        Assert.Contains("COMPLIANT", cut.Find("[data-abom-request-compliant]").TextContent);
        Assert.Contains("NON_COMPLIANT", cut.Find("[data-abom-request-noncompliant]").TextContent);
    }

    [Fact]
    public void ARequestCannotBeSavedWithoutADescriptionAndBothExamples()
    {
        var cut = Render(None);
        cut.Find("[data-abom-request-new]").Click();

        Write(cut, "description", "flag a self-hosted runner");
        Write(cut, "compliant", "runs-on: ubuntu-latest");

        Assert.True(cut.Find("[data-abom-request-save]").HasAttribute("disabled"));
    }

    [Fact]
    public void SavingARequestSendsWhatWasWrittenAndWhoWroteIt()
    {
        var cut = Render(None);
        cut.Find("[data-abom-request-new]").Click();
        Write(cut, "description", "flag a self-hosted runner");
        Write(cut, "compliant", "runs-on: ubuntu-latest");
        Write(cut, "noncompliant", "runs-on: self-hosted");

        cut.Find("[data-abom-request-save]").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "POST" && r.Url.Contains("api/query-requests"));
            Assert.Contains("\"nonCompliant\":\"runs-on: self-hosted\"", sent.Body);
            Assert.Contains("\"requestedBy\":\"operator\"", sent.Body);
        });
    }

    [Fact]
    public void OpeningARequestShowsBothExamples()
    {
        var cut = Render(One);

        cut.Find("[data-abom-request-detail]").Click();

        cut.WaitForAssertion(() =>
        {
            Assert.Contains("runs-on: ubuntu-latest", cut.Find("[data-abom-request-shown-compliant]").TextContent);
            Assert.Contains("runs-on: self-hosted", cut.Find("[data-abom-request-shown-noncompliant]").TextContent);
        });
    }

    [Fact]
    public void EditingARequestSavesItUnderItsOwnNumber()
    {
        var cut = Render(One);
        cut.Find("[data-abom-request-edit]").Click();
        Write(cut, "description", "flag a self-hosted runner on any trigger");

        cut.Find("[data-abom-request-save]").Click();

        cut.WaitForAssertion(() =>
            Assert.Single(Sent, r => r.Method == "PUT" && r.Url.EndsWith("api/query-requests/7")));
    }

    [Fact]
    public void WithNoIdentityARequestCannotBeFiled()
    {
        var cut = Render(None, SignedOut);

        Assert.True(cut.Find("[data-abom-request-new]").HasAttribute("disabled"));
    }
}
