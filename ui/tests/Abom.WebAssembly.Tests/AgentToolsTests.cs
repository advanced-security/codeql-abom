using System.Net;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Settings lists everything an agent session runs on, where each path came from, and lets the
/// operator name the CodeQL and Copilot CLIs; the server checks a path before keeping it.
/// </summary>
public class AgentToolsTests : AbomTestContext
{
    private const string SignedIn = """{"authenticated":true,"login":"operator","accounts":[]}""";

    private const string Readiness = """
        {"known":true,"ready":false,"checks":[
          {"key":"codeql","name":"CodeQL CLI","state":"ready","path":"/opt/codeql/codeql","source":"--codeql",
           "version":"2.27.2","reason":"","remedy":"","saved":"","settable":true},
          {"key":"copilot","name":"Copilot CLI","state":"problem","path":"copilot","source":"--copilot-cli",
           "version":"","reason":"copilot is not on PATH",
           "remedy":"set its path in Settings, or start the server with --copilot-cli or COPILOT_CLI_PATH",
           "saved":"","settable":true},
          {"key":"qlmcp","name":"ql-mcp","state":"ready","path":"npx -y ql-mcp@2.27.2","source":"--ql-mcp",
           "version":"2.27.2","reason":"","remedy":"","saved":"","settable":false}]}
        """;

    private const string CopilotSet = """
        {"known":true,"ready":true,"checks":[
          {"key":"copilot","name":"Copilot CLI","state":"ready","path":"/Users/me/bin/copilot","source":"Settings",
           "version":"1.0.40","reason":"","remedy":"","saved":"","settable":true}]}
        """;

    private const string CodeQLForNextStart = """
        {"known":true,"ready":true,"checks":[
          {"key":"codeql","name":"CodeQL CLI","state":"ready","path":"/opt/codeql/codeql","source":"--codeql",
           "version":"2.27.2","reason":"","remedy":"","saved":"/opt/codeql-2.27.3/codeql","settable":true}]}
        """;

    private IRenderedComponent<Shared.AuthoringReadinessPanel> Render(string readiness)
    {
        Answer("api/authoring/readiness", readiness);
        Answer("api/accounts", SignedIn);
        var cut = Render<Shared.AuthoringReadinessPanel>(p => p.Add(c => c.Editable, true));
        cut.WaitForAssertion(() => cut.Find("[data-abom-readiness]"));
        return cut;
    }

    [Fact]
    public void EveryCheckSaysWhatRunsAndWhereThatCameFrom()
    {
        var cut = Render(Readiness);

        var codeql = cut.Find("[data-abom-tool-check=codeql]").TextContent;
        Assert.Contains("/opt/codeql/codeql", codeql);
        Assert.Contains("--codeql", codeql);
        Assert.Contains("2.27.2", codeql);
        var copilot = cut.Find("[data-abom-tool-check=copilot]").TextContent;
        Assert.Contains("copilot is not on PATH", copilot);
        Assert.Contains("COPILOT_CLI_PATH", copilot);
        Assert.Empty(cut.FindAll("[data-abom-tool-path=qlmcp]"));
    }

    [Fact]
    public void APathIsSentForItsToolAndWhatRunsNowIsShown()
    {
        AnswerWrite("api/authoring/tools/copilot", CopilotSet);
        var cut = Render(Readiness);

        cut.Find("[data-abom-tool-path=copilot] input").Input("/Users/me/bin/copilot");
        cut.Find("[data-abom-tool-save=copilot]").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "PUT" && r.Url.EndsWith("api/authoring/tools/copilot"));
            Assert.Contains("\"path\":\"/Users/me/bin/copilot\"", sent.Body);
            Assert.Contains("\"by\":\"operator\"", sent.Body);
            Assert.Contains("Settings", cut.Find("[data-abom-tool-check=copilot]").TextContent);
        });
    }

    [Fact]
    public void APathTheServerCannotRunIsRefusedWithItsReason()
    {
        RefuseWrite("api/authoring/tools/codeql", HttpStatusCode.BadRequest,
            "not a usable executable: /tmp/codeql is not executable");
        var cut = Render(Readiness);

        cut.Find("[data-abom-tool-path=codeql] input").Input("/tmp/codeql");
        cut.Find("[data-abom-tool-save=codeql]").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains("is not executable", cut.Find("[data-abom-tool-refusal]").TextContent));
    }

    /// <summary>Returning to the default sends an empty path, which forgets the one kept.</summary>
    [Fact]
    public void UsingTheDefaultForgetsThePathKeptInSettings()
    {
        AnswerWrite("api/authoring/tools/copilot", Readiness);
        var cut = Render(CopilotSet);

        cut.Find("[data-abom-tool-default=copilot]").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "PUT" && r.Url.EndsWith("api/authoring/tools/copilot"));
            Assert.Contains("\"path\":\"\"", sent.Body);
        });
    }

    /// <summary>A CodeQL CLI is held by the crawl, so one kept now is used from the next start.</summary>
    [Fact]
    public void ACodeQLPathKeptForTheNextStartSaysSo()
    {
        var cut = Render(CodeQLForNextStart);

        var saved = cut.Find("[data-abom-tool-saved=codeql]").TextContent;
        Assert.Contains("/opt/codeql-2.27.3/codeql", saved);
        Assert.Contains("restart", saved);
    }

    [Fact]
    public void AServerThatCannotSayIsNotReadAsReady()
    {
        var cut = Render("""{"known":false,"ready":false,"checks":[]}""");

        Assert.Contains("cannot say", cut.Find("[data-abom-readiness-unknown]").TextContent);
    }
}
