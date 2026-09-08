using System.Net.Http.Json;
using System.Text.Json;
using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// Reads the ABOM over the API served by `abom serve`.
///
/// Nothing is cached beyond the summary: the database changes while an expansion runs, and a
/// stale coverage figure would misreport how much is known.
/// </summary>
public sealed class AbomApi(HttpClient http)
{
    private static readonly JsonSerializerOptions Json = new()
    {
        PropertyNameCaseInsensitive = true,
    };

    public async Task<Summary> GetSummaryAsync(CancellationToken ct = default) =>
        await GetAsync<Summary>("api/summary", ct) ?? new Summary();

    public async Task<AccountIdentity> GetAccountsAsync(CancellationToken ct = default) =>
        await GetAsync<AccountIdentity>("api/accounts", ct) ?? new AccountIdentity();

    /// <summary>Re-asks gh who the operator is. A POST because it runs a subprocess.</summary>
    public async Task<AccountIdentity> RefreshAccountsAsync(CancellationToken ct = default)
    {
        var response = await http.PostAsync("api/accounts/refresh", content: null, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<AccountIdentity>(Json, ct)
               ?? new AccountIdentity();
    }

    public async Task<IReadOnlyList<OwnerCandidate>> GetOwnerCandidatesAsync(
        CancellationToken ct = default) =>
        await GetListAsync<OwnerCandidate>("api/owner-candidates", ct);

    public async Task<DiscoveryState> GetDiscoveryAsync(CancellationToken ct = default) =>
        await GetAsync<DiscoveryState>("api/discovery/status", ct) ?? new DiscoveryState();

    /// <summary>
    /// Reads what the crawl is trying to achieve and what it has been spending. A loop whose
    /// priorities are invisible is one nobody can steer.
    /// </summary>
    public async Task<Objective?> GetObjectiveAsync(CancellationToken ct = default) =>
        await GetAsync<Objective>("api/discovery/objective", ct);

    /// <summary>
    /// Starts or pauses expansion. A POST because it makes the server clone repositories and
    /// run CodeQL, and the header is what keeps a cross-site form from doing the same.
    /// </summary>
    public async Task<DiscoveryState> SetDiscoveryAsync(bool running, CancellationToken ct = default)
    {
        var request = new HttpRequestMessage(
            HttpMethod.Post, running ? "api/discovery/start" : "api/discovery/pause");
        request.Headers.Add("X-Abom-Control", "1");
        var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<DiscoveryState>(Json, ct)
               ?? new DiscoveryState();
    }

    /// <summary>What each dependency tier runs, and every suite it could be bound to.</summary>
    public async Task<SuiteBinding?> GetTierSuitesAsync(CancellationToken ct = default) =>
        await GetAsync<SuiteBinding>("api/discovery/suites", ct);

    /// <summary>
    /// Rewrites what one tier runs. Takes effect on the next round rather than the current one,
    /// because a round's queries are resolved once before any repository is claimed.
    /// </summary>
    public async Task<SuiteBinding?> SetTierSuitesAsync(
        string tier, IReadOnlyList<string> suites, CancellationToken ct = default)
    {
        var request = new HttpRequestMessage(
            HttpMethod.Put, $"api/discovery/suites/{Uri.EscapeDataString(tier)}")
        {
            Content = JsonContent.Create(suites),
        };
        request.Headers.Add("X-Abom-Control", "1");
        var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<SuiteBinding>(Json, ct);
    }

    /// <summary>
    /// Puts a whole dependency tier back in the queue, so a changed or extended query suite can
    /// be run over work already analyzed. Nothing already known is cleared.
    /// </summary>
    public async Task<DiscoveryState> RequeueTierAsync(string tier, CancellationToken ct = default)
    {
        var request = new HttpRequestMessage(
            HttpMethod.Post, $"api/discovery/requeue/{Uri.EscapeDataString(tier)}");
        request.Headers.Add("X-Abom-Control", "1");
        var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<DiscoveryState>(Json, ct)
               ?? new DiscoveryState();
    }

    /// <summary>
    /// Puts back only what has not been analyzed at the identity the bound suites resolve to
    /// now. The server resolves that identity through CodeQL, which is why it cannot be
    /// previewed on the button the way a tier's size can.
    /// </summary>
    public Task<RequeueResult> RequeueStaleAsync(CancellationToken ct = default) =>
        RequeueAsync("api/discovery/requeue-stale", null, ct);

    /// <summary>
    /// Puts back the dependencies one repository answers, ahead of the ranking. The tier a
    /// dependency sits in is not the granularity anyone reasons about after fixing something.
    /// </summary>
    public Task<RequeueResult> RequeueRepositoryAsync(
        string repository, CancellationToken ct = default) =>
        RequeueAsync("api/discovery/requeue-repository", new { repository }, ct);

    private async Task<RequeueResult> RequeueAsync(string path, object? body, CancellationToken ct)
    {
        var request = new HttpRequestMessage(HttpMethod.Post, path);
        request.Headers.Add("X-Abom-Control", "1");
        if (body is not null)
        {
            request.Content = JsonContent.Create(body);
        }
        var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<RequeueResult>(Json, ct)
               ?? new RequeueResult();
    }

    public async Task<GraphVersion> GetGraphVersionAsync(CancellationToken ct = default) =>
        await GetAsync<GraphVersion>("api/graph/version", ct) ?? new GraphVersion();

    public async Task<IReadOnlyList<RemediationItem>> GetRemediationAsync(
        int top = 50, CancellationToken ct = default) =>
        await GetListAsync<RemediationItem>($"api/remediation?top={top}", ct);

    public async Task<IReadOnlyList<AdvisoryReach>> GetAdvisoriesAsync(
        int top = 50, CancellationToken ct = default) =>
        await GetListAsync<AdvisoryReach>($"api/advisories?top={top}", ct);

    public async Task<IReadOnlyList<ExposureSummary>> GetExposureByAdvisoryAsync(
        CancellationToken ct = default) =>
        await GetListAsync<ExposureSummary>("api/exposure?by=advisory", ct);

    public async Task<IReadOnlyList<Exposure>> GetExposuresAsync(
        int top = 200, CancellationToken ct = default) =>
        await GetListAsync<Exposure>($"api/exposure?by=repository&top={top}", ct);

    public async Task<IReadOnlyList<ActionSummary>> GetActionsAsync(
        string? search = null, int top = 200, CancellationToken ct = default) =>
        await GetListAsync<ActionSummary>(
            $"api/actions?top={top}&q={Uri.EscapeDataString(search ?? string.Empty)}", ct);

    public async Task<IReadOnlyList<RepositorySummary>> GetRepositoriesAsync(
        string? search = null, int top = 500, CancellationToken ct = default) =>
        await GetListAsync<RepositorySummary>(
            $"api/repositories?top={top}&q={Uri.EscapeDataString(search ?? string.Empty)}", ct);

    public async Task<RepositoryDetail?> GetRepositoryAsync(
        string fullName, CancellationToken ct = default) =>
        await GetAsync<RepositoryDetail>($"api/repositories/{fullName}", ct);

    // The key contains slashes of its own, so it is appended as a path rather than escaped.
    public async Task<NodeDetail?> GetNodeAsync(string key, CancellationToken ct = default) =>
        await GetAsync<NodeDetail>($"api/node/{key}", ct);

    public async Task<BumpImpact?> GetBumpImpactAsync(
        string key, CancellationToken ct = default) =>
        await GetAsync<BumpImpact>($"api/node/{key}/-/impact", ct);

    public async Task<IReadOnlyList<NodeRoute>> GetReachAsync(
        string key, CancellationToken ct = default) =>
        await GetListAsync<NodeRoute>($"api/node/{key}/-/reach", ct);

    public async Task<IReadOnlyList<DeclaredAccount>> GetOwnedScopeAsync(
        CancellationToken ct = default) =>
        await GetListAsync<DeclaredAccount>("api/owned-scope", ct);

    /// <summary>
    /// Replaces the declaration wholesale, matching the store: a diff nobody assembled is a diff
    /// nobody can check, so the caller sends the scope it wants to end up with.
    /// </summary>
    public async Task<IReadOnlyList<DeclaredAccount>> SetOwnedScopeAsync(
        IReadOnlyList<DeclaredAccount> accounts, CancellationToken ct = default)
    {
        var request = new HttpRequestMessage(HttpMethod.Put, "api/owned-scope")
        {
            Content = JsonContent.Create(accounts),
        };
        request.Headers.Add("X-Abom-Control", "1");
        var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<IReadOnlyList<DeclaredAccount>>(Json, ct)
               ?? [];
    }

    /// <summary>
    /// Asks what a candidate declaration would admit and drop. Carries no control header because
    /// it writes nothing, and guarding a read would tell the next reader that it does.
    /// </summary>
    public async Task<ScopePreview?> PreviewOwnedScopeAsync(
        IReadOnlyList<DeclaredAccount> accounts, CancellationToken ct = default)
    {
        var response = await http.PostAsJsonAsync("api/owned-scope/preview", accounts, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<ScopePreview>(Json, ct);
    }

    /// <summary>
    /// Reads the bill of materials. No roots asks for the declared owned scope, which is the
    /// usual case and the only one that needs no argument.
    /// </summary>
    public async Task<Bom?> GetBomAsync(
        IEnumerable<string>? roots = null, CancellationToken ct = default)
    {
        var query = string.Join("&", (roots ?? []).Select(
            r => "root=" + Uri.EscapeDataString(r)));
        var path = string.IsNullOrEmpty(query) ? "api/bom" : $"api/bom?{query}";
        return await GetAsync<Bom>(path, ct);
    }

    /// <summary>
    /// The address a download link points at. Built here rather than in markup so the page and
    /// the API cannot drift apart about what the parameters are called.
    /// </summary>
    public static string BomDownloadUrl(string format, IEnumerable<string>? roots = null)
    {
        var parts = new List<string> { "format=" + Uri.EscapeDataString(format), "download=true" };
        parts.AddRange((roots ?? []).Select(r => "root=" + Uri.EscapeDataString(r)));
        return "api/bom?" + string.Join("&", parts);
    }

    // The reference goes in the query string rather than the path: it may be empty, and an empty
    // path segment cannot say "default branch" apart from a malformed route.
    public async Task<NodeUsers?> GetNodeUsersAsync(
        string key, string reference, CancellationToken ct = default) =>
        await GetAsync<NodeUsers>(
            $"api/node/{key}/-/users?ref={Uri.EscapeDataString(reference)}", ct);

    public async Task<IReadOnlyList<PinningRow>> GetPinningAsync(
        string scope, string? parent, PinningQuery filter, CancellationToken ct = default) =>
        await GetListAsync<PinningRow>(
            $"api/pinning?scope={Uri.EscapeDataString(scope)}" +
            $"&parent={Uri.EscapeDataString(parent ?? string.Empty)}{filter.Query}", ct);

    public async Task<PinningRow?> GetPinningTotalsAsync(
        PinningQuery filter, CancellationToken ct = default) =>
        await GetAsync<PinningRow>($"api/pinning/totals?scope=all{filter.Query}", ct);

    public async Task<IReadOnlyList<FrontierItem>> GetFrontierAsync(
        string? status = null, int top = 500, CancellationToken ct = default) =>
        await GetListAsync<FrontierItem>(
            $"api/frontier?top={top}&status={Uri.EscapeDataString(status ?? string.Empty)}", ct);

    public async Task<Subgraph> GetGraphAsync(
        string focus, int hops, string granularity, string direction,
        int max = 2000, CancellationToken ct = default)
    {
        var url = $"api/graph?focus={Uri.EscapeDataString(focus)}&hops={hops}" +
                  $"&granularity={granularity}&direction={direction}&max={max}";
        return await GetAsync<Subgraph>(url, ct) ?? new Subgraph { Focus = focus };
    }

    private async Task<T?> GetAsync<T>(string url, CancellationToken ct)
    {
        try
        {
            var result = await http.GetFromJsonAsync<T>(url, Json, ct);
            // Cleared on success, or a single transient failure would be reported for the rest
            // of the session.
            LastError = null;
            return result;
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{url}: {ex.Message}";
            return default;
        }
    }

    private async Task<IReadOnlyList<T>> GetListAsync<T>(string url, CancellationToken ct) =>
        await GetAsync<List<T>>(url, ct) ?? [];

    /// <summary>The most recent transport failure, surfaced rather than swallowed.</summary>
    public string? LastError { get; private set; }
}
