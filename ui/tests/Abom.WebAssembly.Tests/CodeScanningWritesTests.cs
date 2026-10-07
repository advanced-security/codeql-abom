using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The Actions code scanning panel says what was read before anything else, previews before it
/// writes, and offers to write only on a server started to write. A preview changes nothing on
/// GitHub, so it is offered everywhere; an approval and a batch are not.
/// </summary>
public class CodeScanningWritesTests : AbomTestContext
{
    private const string Readings = """
        {"counts":{"covered":7,"add":1,"enable":2,"advanced":7,"conflict":1,"not-owned":204},
         "declared":33,"read":31,"oldestRead":"2026-10-05T23:50:00Z","items":[]}
        """;

    private const string Views = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]
        """;

    private const string Preview = """
        {"id":4,"createdAt":"2026-10-06T00:00:00Z","createdBy":"interface","viewName":"owned",
         "predicate":"scope=owned","accounts":["has-ghas/test-repo-2"],"excluded":[],
         "approvedBy":"","batchId":0,"counts":{"enable":1,"not-a-target":1}}
        """;

    private const string PreviewRows = """
        {"items":[
           {"repository":"has-ghas/test-repo-2","class":"enable","evidence":"default setup is off; GitHub would analyze actions",
            "languages":["actions"],"readAt":"2026-10-05T23:50:00Z",
            "intended":"{\"state\":\"configured\",\"languages\":[\"actions\"]}"},
           {"repository":"has-ghas/dubbo","class":"not-a-target",
            "evidence":"outside the write targets, so never written from here: default setup analyzes java-kotlin without actions",
            "languages":["java-kotlin"],"readAt":"2026-10-05T23:50:00Z","intended":""}],
         "total":2}
        """;

    private void ReadingsAndViews(bool writes)
    {
        Answer("api/code-scanning/readings", Readings);
        Answer("api/code-scanning/writes", writes
            ? """{"writes":true,"targets":["has-ghas/test-repo-2"],"accounts":[]}"""
            : """{"writes":false,"targets":[],"accounts":[]}""");
        Answer("api/views", Views);
        Answer("api/code-scanning/batches", "[]");
        Answer("api/code-scanning/ledger", """{"items":[],"total":0}""");
        Answer("api/code-scanning/previews/4/rows", PreviewRows);
    }

    [Fact]
    public void WhatWasReadComesBeforeAnythingThatWouldWrite()
    {
        ReadingsAndViews(writes: false);
        Answer("api/code-scanning/previews", "[]");
        var cut = Render<Shared.CodeScanningWrites>();

        cut.WaitForAssertion(() =>
        {
            var readings = cut.Find("[data-abom-setup-readings]").TextContent;
            Assert.Contains("31 of 33", readings);
            Assert.Contains("1 to add actions to", readings);
            Assert.Contains("2 to enable", readings);
            Assert.Contains(TimeText.DayAndTime("2026-10-05T23:50:00Z"), readings);
            Assert.DoesNotContain("2026-10-05T23:50", readings);
            var mode = cut.Find("[data-abom-setup-mode]").TextContent;
            Assert.Contains("writes nothing", mode);
            Assert.Contains("--enable-targets", mode);
        });
        Assert.Contains(Sent, r => r.Url.Contains("api/code-scanning/readings?summary=true"));
    }

    [Fact]
    public void APreviewIsOfferedEverywhereAndApprovalOnlyWhereTheServerWrites()
    {
        ReadingsAndViews(writes: false);
        Answer("api/code-scanning/previews", "[]");
        AnswerWrite("api/code-scanning/previews", Preview);
        var cut = Render<Shared.CodeScanningWrites>();
        cut.WaitForElement("[data-abom-setup-preview-button]").Click();

        cut.WaitForAssertion(() =>
        {
            Assert.Contains(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/code-scanning/previews"));
            Assert.Contains("has-ghas/test-repo-2", cut.Find("[data-abom-setup-preview]").TextContent);
            Assert.Contains("never written from here", cut.Find("[data-abom-setup-preview]").TextContent);
            Assert.Empty(cut.FindAll("[data-abom-setup-approve]"));
        });
    }

    /// <summary>
    /// A preview of an organization is twenty thousand rows, so the panel asks for one page of them,
    /// says how many there are, and names each class in words rather than the store's.
    /// </summary>
    [Fact]
    public void APreviewPagesItsRowsAndNamesEachClassInWords()
    {
        ReadingsAndViews(writes: false);
        Answer("api/code-scanning/previews", "[]");
        AnswerWrite("api/code-scanning/previews", Preview);
        Answer("api/code-scanning/previews/4/rows", PreviewRows.Replace("\"total\":2", "\"total\":20412"));
        var cut = Render<Shared.CodeScanningWrites>();
        cut.WaitForElement("[data-abom-setup-preview-button]").Click();

        cut.WaitForAssertion(() =>
        {
            var asked = Assert.Single(Sent, r => r.Url.Contains("api/code-scanning/previews/4/rows"));
            Assert.Contains("offset=0", asked.Url);
            Assert.Contains("top=", asked.Url);
            Assert.Contains("of 20,412", cut.Find("[data-abom-setup-preview]").TextContent);
            Assert.Equal("Enable, with actions alone",
                cut.Find("[data-abom-setup-row-class='has-ghas/test-repo-2']").TextContent.Trim());
            Assert.Equal("Not a write target",
                cut.Find("[data-abom-setup-row-class='has-ghas/dubbo']").TextContent.Trim());
        });
    }

    [Fact]
    public void AnApprovedPreviewStartsItsBatchOnAServerThatWrites()
    {
        ReadingsAndViews(writes: true);
        AnswerWrite("api/code-scanning/previews/4/approve",
            Preview.Replace("\"approvedBy\":\"\"", "\"approvedBy\":\"data-douser\""));
        AnswerWrite("api/code-scanning/batches", """
            {"id":9,"previewId":4,"startedAt":"2026-10-06T00:01:00Z","startedBy":"interface","state":"running",
             "steps":{},"remaining":1}
            """);
        Answer("api/code-scanning/previews", "[]");
        AnswerWrite("api/code-scanning/previews", Preview);
        var cut = Render<Shared.CodeScanningWrites>();
        cut.WaitForElement("[data-abom-setup-preview-button]").Click();
        cut.WaitForElement("[data-abom-setup-approve]").Click();
        cut.WaitForElement("[data-abom-setup-start]").Click();

        cut.WaitForAssertion(() =>
        {
            Assert.Equal("This server writes default setup, with your own token, to has-ghas/test-repo-2, " +
                         "once a preview listing them is approved.",
                cut.Find("[data-abom-setup-mode]").TextContent.Trim());
            Assert.Contains(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/code-scanning/previews/4/approve"));
            var start = Assert.Single(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/code-scanning/batches"));
            Assert.Contains("\"preview\":4", start.Body);
            Assert.Contains("approved by data-douser", cut.Find("[data-abom-setup-preview]").TextContent);
        });
    }

    [Fact]
    public void ABatchSaysWhereItStandsAndTheLedgerSaysEveryStep()
    {
        ReadingsAndViews(writes: true);
        Answer("api/code-scanning/previews", "[]");
        Answer("api/code-scanning/batches", """
            [{"id":9,"previewId":4,"startedAt":"2026-10-06T00:01:00Z","startedBy":"interface","state":"paused",
              "reason":"2 writes in a row failed","steps":{"sent":1,"failed":2},"remaining":3}]
            """);
        Answer("api/code-scanning/ledger", """
            {"items":[{"id":1,"batchId":9,"repository":"has-ghas/test-repo-2","at":"2026-10-06T00:02:00Z","step":"sent",
              "status":202,"before":[],"after":[],"runId":42}],"total":120}
            """);
        var cut = Render<Shared.CodeScanningWrites>();

        cut.WaitForAssertion(() =>
        {
            var batch = cut.Find("[data-abom-setup-batch='9']").TextContent;
            Assert.Contains("paused", batch);
            Assert.Contains("2 writes in a row failed", batch);
            Assert.Contains("3 left", batch);
            var ledger = cut.Find("[data-abom-setup-ledger]").TextContent;
            Assert.Contains("has-ghas/test-repo-2", ledger);
            Assert.Contains(TimeText.DayAndTime("2026-10-06T00:02:00Z", "HH:mm:ss"), ledger);
            Assert.DoesNotContain("2026-10-06T00:02", ledger);
            Assert.Contains("1 of 120", cut.Find("[data-abom-setup-ledger-said]").TextContent);
            Assert.Contains(Sent, r => r.Url.Contains("api/code-scanning/ledger?top="));
            Assert.NotEmpty(cut.FindAll("[data-abom-setup-act='resume']"));
        });
    }
}
