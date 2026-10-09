using System.Net;
using System.Text.Json;
using Abom.WebAssembly.Models;
using Abom.WebAssembly.Services;
using Xunit;

namespace Abom.WebAssembly.Tests;

public sealed class ApiControlTests
{
    [Theory]
    [InlineData("test", "POST", "/api/queries/test", "this test run")]
    [InlineData("cancel", "POST", "/api/runs/7/cancel", "to stop this run")]
    [InlineData("pool", "PUT", "/api/pools", "it")]
    [InlineData("scanning", "POST", "/api/code-scanning/previews", "it")]
    public async Task GuardedRequestsKeepTheirMethodBodyAndRefusal(
        string action, string method, string path, string refused)
    {
        using var handler = new Handler(async request =>
        {
            Assert.Equal(method, request.Method.Method);
            Assert.Equal(path, request.RequestUri!.AbsolutePath);
            Assert.Equal("1", Assert.Single(request.Headers.GetValues("X-Abom-Control")));
            if (action == "test")
            {
                using var body = JsonDocument.Parse(await request.Content!.ReadAsStringAsync(TestContext.Current.CancellationToken));
                Assert.Equal("acme/test", body.RootElement.GetProperty("queryId").GetString());
                Assert.Equal("v1", body.RootElement.GetProperty("queryVersion").GetString());
            }
            if (action == "cancel") Assert.Null(request.Content);
            if (action == "scanning") Assert.Equal("{}", await request.Content!.ReadAsStringAsync(TestContext.Current.CancellationToken));
            return Response(HttpStatusCode.Conflict, "{\"error\":null}");
        });
        using var http = new HttpClient(handler) { BaseAddress = new Uri("http://localhost/") };
        var api = new AbomApi(http);
        Assert.Equal($"the server refused {refused} (409)", await Call(api, action));
        Assert.Null(api.LastError);
    }

    [Theory]
    [InlineData("test", "api/queries/test", false)]
    [InlineData("cancel", "api/runs/7/cancel", false)]
    [InlineData("pool", "api/pools", true)]
    [InlineData("scanning", "api/code-scanning/previews", true)]
    public async Task TransportFailuresKeepTheExistingContext(
        string action, string path, bool contextual)
    {
        using var handler = new Handler(_ => throw new HttpRequestException("offline"));
        using var http = new HttpClient(handler) { BaseAddress = new Uri("http://localhost/") };
        var api = new AbomApi(http);
        Assert.Equal(contextual ? $"{path}: offline" : "offline", await Call(api, action));
        Assert.Equal($"{path}: offline", api.LastError);
    }

    [Theory]
    [InlineData("start", "/api/discovery/start", "POST")]
    [InlineData("pause", "/api/discovery/pause", "POST")]
    [InlineData("resume", "/api/discovery/resume-on-start", "POST")]
    [InlineData("suites", "/api/discovery/suites/owned", "PUT")]
    [InlineData("requeue", "/api/discovery/requeue/owned", "POST")]
    [InlineData("retry", "/api/discovery/retry-unavailable", "POST")]
    [InlineData("prune", "/api/prune/apply", "POST")]
    [InlineData("enumerate", "/api/owned-scope/enumerate", "POST")]
    public async Task ThrowingControlsNeverTurnARefusalIntoSuccess(string action, string path, string method)
    {
        using var handler = new Handler(request =>
        {
            Assert.Equal(path, request.RequestUri!.AbsolutePath);
            Assert.Equal(method, request.Method.Method);
            Assert.Equal("1", Assert.Single(request.Headers.GetValues("X-Abom-Control")));
            return Task.FromResult(Response(HttpStatusCode.Forbidden, "{\"error\":\"held\"}"));
        });
        using var http = new HttpClient(handler) { BaseAddress = new Uri("http://localhost/") };
        var api = new AbomApi(http);
        var ct = TestContext.Current.CancellationToken;
        await Assert.ThrowsAsync<HttpRequestException>(() => action switch
        {
            "start" => api.SetDiscoveryAsync(true, ct),
            "pause" => api.SetDiscoveryAsync(false, ct),
            "resume" => api.SetResumeOnStartAsync(true, ct),
            "suites" => api.SetTierSuitesAsync("owned", ["suite"], ct),
            "requeue" => api.RequeueTierAsync("owned", ct),
            "retry" => api.RetryUnavailableAsync(ct),
            "prune" => api.PruneAsync(false, ct),
            "enumerate" => api.EnumerateOwnedScopeAsync(ct),
            _ => throw new InvalidOperationException(action),
        });
    }

    private static async Task<string?> Call(AbomApi api, string action) => action switch
    {
        "test" => (await api.TestQueryAsync("acme/test", "v1", TestContext.Current.CancellationToken)).Refusal,
        "cancel" => (await api.CancelAnalysisRunAsync(7, TestContext.Current.CancellationToken)).Refusal,
        "pool" => (await api.SetPoolRateAsync("git", "requests", 42, 2, TestContext.Current.CancellationToken)).Refusal,
        "scanning" => (await api.SendCodeScanningAsync<SetupPreview>("api/code-scanning/previews", null, TestContext.Current.CancellationToken)).Refusal,
        _ => throw new InvalidOperationException(action),
    };

    [Theory]
    [InlineData("run", "api/runs", "POST")]
    [InlineData("execute", "api/runs/7/start", "POST")]
    [InlineData("advance", "api/queries/stage", "POST")]
    [InlineData("request", "api/query-requests", "POST")]
    [InlineData("databases", "api/databases?scope=owned", "GET")]
    [InlineData("page", "api/actions?top=1&offset=0", "GET")]
    public async Task UnguardedRequestsKeepTransportErrorsSeparateFromRefusals(
        string action, string path, string method)
    {
        using var handler = new Handler(request =>
        {
            Assert.False(request.Headers.Contains("X-Abom-Control"));
            Assert.Equal(method, request.Method.Method);
            throw new HttpRequestException("offline");
        });
        using var http = new HttpClient(handler) { BaseAddress = new Uri("http://localhost/") };
        var api = new AbomApi(http);
        var ct = TestContext.Current.CancellationToken;
        string? refusal = action switch
        {
            "run" => (await api.StartAnalysisRunAsync("scope=owned", [], "operator", false, ct)).Refusal,
            "execute" => (await api.ExecuteAnalysisRunAsync(7, ct)).Refusal,
            "advance" => (await api.AdvanceQueryStageAsync("acme/test", "v1", "test", "operator", ct)).Refusal,
            "request" => (await api.SaveQueryRequestAsync(new QueryRequest(), ct)).Refusal,
            "databases" => (await api.GetDatabasesAsync("scope=owned", ct)).Refusal,
            "page" => (await api.GetPageAsync<object>("api/actions", "", 1, 0, null, false, ct)).Refusal,
            _ => throw new InvalidOperationException(action),
        };
        Assert.Null(refusal);
        Assert.Equal($"{path}: offline", api.LastError);
    }

    private static HttpResponseMessage Response(HttpStatusCode status, string body) =>
        new(status) { Content = new StringContent(body, System.Text.Encoding.UTF8, "application/json") };

    private sealed class Handler(Func<HttpRequestMessage, Task<HttpResponseMessage>> send) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request, CancellationToken cancellationToken) => send(request);
    }
}
