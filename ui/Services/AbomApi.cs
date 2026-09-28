using System.Diagnostics;
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
        using var response = await http.PostAsync("api/accounts/refresh", content: null, ct);
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
        using var request = new HttpRequestMessage(
            HttpMethod.Post, running ? "api/discovery/start" : "api/discovery/pause");
        request.Headers.Add("X-Abom-Control", "1");
        using var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<DiscoveryState>(Json, ct)
               ?? new DiscoveryState();
    }

    /// <summary>
    /// Records whether a server start may resume a crawl that was running. Guarded like the
    /// start control, because it decides what a future start spends with nobody present.
    /// </summary>
    public async Task<DiscoveryState> SetResumeOnStartAsync(
        bool resume, CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(
            HttpMethod.Post, "api/discovery/resume-on-start")
        {
            Content = JsonContent.Create(new { resume }),
        };
        request.Headers.Add("X-Abom-Control", "1");
        using var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<DiscoveryState>(Json, ct)
               ?? new DiscoveryState();
    }

    /// <summary>
    /// Runs a version's tests and records the verdict. ABOM never forms the opinion itself:
    /// `codeql test run` compares the query's output to the checked-in `.expected`.
    /// </summary>
    public async Task<(QueryCatalog? Catalog, string? Refusal)> TestQueryAsync(
        string queryId, string version, CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, "api/queries/test")
        {
            Content = JsonContent.Create(new { queryId, queryVersion = version }),
        };
        request.Headers.Add("X-Abom-Control", "1");
        try
        {
            using var response = await http.SendAsync(request, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<QueryCatalog>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (null, refusal?.Error
                ?? $"the server refused this test run ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"api/queries/test: {ex.Message}";
            return (null, ex.Message);
        }
    }

    /// <summary>
    /// Stops a run that is under way. What has already been analyzed is kept: the executor
    /// observes cancellation between members rather than discarding the work behind it.
    /// </summary>
    public async Task<(AnalysisRun? Run, string? Refusal)> CancelAnalysisRunAsync(
        long id, CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, $"api/runs/{id}/cancel");
        request.Headers.Add("X-Abom-Control", "1");
        try
        {
            using var response = await http.SendAsync(request, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<AnalysisRun>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (null, refusal?.Error
                ?? $"the server refused to stop this run ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"api/runs/{id}/cancel: {ex.Message}";
            return (null, ex.Message);
        }
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
        using var request = new HttpRequestMessage(
            HttpMethod.Put, $"api/discovery/suites/{Uri.EscapeDataString(tier)}")
        {
            Content = JsonContent.Create(suites),
        };
        request.Headers.Add("X-Abom-Control", "1");
        using var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<SuiteBinding>(Json, ct);
    }

    /// <summary>
    /// Puts a whole dependency tier back in the queue, so a changed or extended query suite can
    /// be run over work already analyzed. Nothing already known is cleared.
    /// </summary>
    public async Task<DiscoveryState> RequeueTierAsync(string tier, CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(
            HttpMethod.Post, $"api/discovery/requeue/{Uri.EscapeDataString(tier)}");
        request.Headers.Add("X-Abom-Control", "1");
        using var response = await http.SendAsync(request, ct);
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

    /// <summary>
    /// Runs GitHub's no-fix lock verification of one repository's current commit. The server
    /// budgets it and says why when it refuses, which is returned rather than thrown.
    /// </summary>
    public async Task<(LockVerification? Result, string Refusal)> VerifyLockAsync(
        string repository, string mode, CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, "api/locks/verify");
        request.Headers.Add("X-Abom-Control", "1");
        request.Content = JsonContent.Create(new { repository, mode });
        using var response = await http.SendAsync(request, ct);
        if (!response.IsSuccessStatusCode)
        {
            var reason = await response.Content.ReadAsStringAsync(ct);
            return (null, string.IsNullOrWhiteSpace(reason) ? response.ReasonPhrase ?? "refused" : reason);
        }
        return (await response.Content.ReadFromJsonAsync<LockVerification>(Json, ct), "");
    }

    private async Task<RequeueResult> RequeueAsync(string path, object? body, CancellationToken ct)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, path);
        request.Headers.Add("X-Abom-Control", "1");
        if (body is not null)
        {
            request.Content = JsonContent.Create(body);
        }
        using var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<RequeueResult>(Json, ct)
               ?? new RequeueResult();
    }

    public async Task<GraphVersion> GetGraphVersionAsync(CancellationToken ct = default) =>
        await GetAsync<GraphVersion>("api/graph/version", ct) ?? new GraphVersion();

    /// <summary>What leaving the declared closure would remove. Changes nothing.</summary>
    public async Task<PruneView?> GetPruneAsync(CancellationToken ct = default) =>
        await GetAsync<PruneView>("api/prune", ct);

    public async Task<Staleness?> GetStalenessAsync(CancellationToken ct = default) =>
        await GetAsync<Staleness>("api/prune/staleness", ct);

    /// <summary>
    /// Tombstones what the declaration does not reach, or deletes what is already tombstoned.
    /// Both carry the control header: they remove work that only re-analysis can rebuild.
    /// </summary>
    public async Task<PruneView?> PruneAsync(bool compact, CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(
            HttpMethod.Post, compact ? "api/prune/compact" : "api/prune/apply");
        request.Headers.Add("X-Abom-Control", "1");
        using var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<PruneView>(Json, ct);
    }

    public async Task<IReadOnlyList<RemediationItem>> GetRemediationAsync(
        int top = 50, CancellationToken ct = default) =>
        (await GetAdvisoriesPageAsync("", top, 0, null, false, ct)).Page?.Items ?? [];

    /// <summary>
    /// One page of the advisories unit, worst first across the whole match.
    /// </summary>
    public async Task<(AdvisoriesPage? Page, string? Refusal)> GetAdvisoriesPageAsync(
        string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct = default) =>
        await GetPageAsync<AdvisoriesPage>("api/remediation", predicate, top, offset, sort, desc, ct);

    /// <summary>
    /// One page of the relationships unit, ordered across the whole match.
    /// </summary>
    public async Task<(RelationshipsPage? Page, string? Refusal)> GetRelationshipsPageAsync(
        string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct = default) =>
        await GetPageAsync<RelationshipsPage>(
            "api/relationships", predicate, top, offset, sort, desc, ct);

    /// <summary>
    /// What a reader can narrow by, and what to call it.
    /// </summary>
    ///
    /// Answered from the language rather than the corpus, so it holds before anything is crawled:
    /// a filter list that waits on a crawl reads as an interface that is broken rather than empty.
    public async Task<IReadOnlyList<FilterTerm>> GetFilterTermsAsync(CancellationToken ct = default) =>
        await GetListAsync<FilterTerm>("api/filters", ct);

    /// <summary>
    /// How many values each term has, so a control can say so before a reader opens it. A term
    /// missing from the answer is one nobody counted, which a caller must not read as none.
    /// </summary>
    public async Task<IReadOnlyDictionary<string, int>> GetFilterValueCountsAsync(
        CancellationToken ct = default) =>
        await GetAsync<Dictionary<string, int>>("api/filters/counts", ct)
        ?? new Dictionary<string, int>(StringComparer.Ordinal);

    /// <summary>
    /// The named views on offer: the built-in catalog merged with whatever the operator saved.
    /// </summary>
    public async Task<IReadOnlyList<NamedView>> GetViewsAsync(CancellationToken ct = default) =>
        await GetListAsync<NamedView>("api/views", ct);

    /// <summary>Every query version the ledger has a run for, and the lifecycle it is staged against.</summary>
    public async Task<QueryCatalog> GetQueryCatalogAsync(CancellationToken ct = default) =>
        await GetAsync<QueryCatalog>("api/queries", ct) ?? new QueryCatalog();

    /// <summary>
    /// Asks for a run of query versions over the databases a predicate resolves to.
    ///
    /// The predicate travels in the query string so the server reads it with the same code the
    /// graph and the gate read it with; a second way to express a scope is a second policy engine.
    /// </summary>
    public async Task<(AnalysisRun? Run, string? Refusal)> StartAnalysisRunAsync(
        string predicate, IReadOnlyList<AnalysisQueryRef> queries, string by,
        bool held, CancellationToken ct = default)
    {
        try
        {
            using var response = await http.PostAsJsonAsync("api/runs?" + predicate,
                new { queries, by, held }, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<AnalysisRun>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (null, refusal?.Error
                ?? $"the server refused this run ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"api/runs: {ex.Message}";
            return (null, null);
        }
    }

    /// <summary>What has been asked for, newest first.</summary>
    public async Task<IReadOnlyList<AnalysisRun>> GetAnalysisRunsAsync(
        CancellationToken ct = default) =>
        (await GetAsync<AnalysisRunList>("api/runs", ct))?.Runs ?? [];

    /// <summary>
    /// What a run over <paramref name="predicate"/> would cover, before anything runs. The server's
    /// refusal comes back verbatim, since it names the term a run cannot apply.
    /// </summary>
    public async Task<(DatabaseSet? Set, string? Refusal)> GetDatabasesAsync(
        string predicate, CancellationToken ct = default)
    {
        var url = "api/databases?" + predicate;
        try
        {
            using var response = await http.GetAsync(url, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<DatabaseSet>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (null, refusal?.Error
                ?? $"the server refused this scope ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{url}: {ex.Message}";
            return (null, null);
        }
    }

    /// <summary>Every query request, newest first, with the vocabularies a form offers.</summary>
    public async Task<QueryRequestList> GetQueryRequestsAsync(CancellationToken ct = default) =>
        await GetAsync<QueryRequestList>("api/query-requests", ct) ?? new QueryRequestList();

    /// <summary>A refusal is the reader leaving something out, so it is returned, not thrown.</summary>
    public async Task<(QueryRequestList? List, string? Refusal)> SaveQueryRequestAsync(
        QueryRequest request, CancellationToken ct = default)
    {
        var path = request.RowId == 0 ? "api/query-requests" : $"api/query-requests/{request.RowId}";
        try
        {
            using var response = request.RowId == 0
                ? await http.PostAsJsonAsync(path, request, ct)
                : await http.PutAsJsonAsync(path, request, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<QueryRequestList>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (null, refusal?.Error
                ?? $"the server refused this request ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{path}: {ex.Message}";
            return (null, null);
        }
    }

    /// <summary>One run with every member, which is where a reader sees what it could not cover.</summary>
    public async Task<AnalysisRun?> GetAnalysisRunAsync(long id, CancellationToken ct = default) =>
        await GetAsync<AnalysisRun>($"api/runs/{id}", ct);

    /// <summary>A request's agent sessions, and the CodeQL its live one has under way.</summary>
    public async Task<AuthoringSessionList?> GetAuthoringSessionsAsync(long request, CancellationToken ct = default) =>
        await GetAsync<AuthoringSessionList>($"api/query-requests/{request}/sessions", ct);

    /// <summary>
    /// Starts an agent session on a request. A refusal names what stands in the way, such as
    /// discovery holding CodeQL, so it is returned rather than thrown.
    /// </summary>
    public Task<(AuthoringSession? Session, string? Refusal)> StartAuthoringSessionAsync(
        long request, string by, CancellationToken ct = default) =>
        ControlAsync<AuthoringSession>($"api/query-requests/{request}/sessions", new { by }, ct);

    /// <summary>Stops the session working a request.</summary>
    public Task<(AuthoringSessionList? List, string? Refusal)> StopAuthoringSessionAsync(
        long request, CancellationToken ct = default) =>
        ControlAsync<AuthoringSessionList>($"api/query-requests/{request}/sessions/stop", null, ct);

    /// <summary>A session's timeline from after the last event the reader holds.</summary>
    public async Task<AuthoringEventPage?> GetAuthoringEventsAsync(
        long session, long after, CancellationToken ct = default) =>
        await GetAsync<AuthoringEventPage>($"api/authoring-sessions/{session}/events?after={after}", ct);

    /// <summary>The trees a request's sessions printed, without their bodies.</summary>
    public async Task<IReadOnlyList<AuthoringAst>> GetAuthoringAstsAsync(long request, CancellationToken ct = default) =>
        (await GetAsync<AuthoringAstList>($"api/query-requests/{request}/asts", ct))?.Asts ?? [];

    /// <summary>One printed tree, body and all.</summary>
    public async Task<AuthoringAst?> GetAuthoringAstAsync(long id, CancellationToken ct = default) =>
        await GetAsync<AuthoringAst>($"api/authoring-asts/{id}", ct);

    /// <summary>The trees printed for a query, by a session or by ABOM, without their bodies.</summary>
    public async Task<IReadOnlyList<AuthoringAst>> GetQueryAstsAsync(string queryId, CancellationToken ct = default) =>
        (await GetAsync<AuthoringAstList>($"api/queries/asts?query={Uri.EscapeDataString(queryId)}", ct))?.Asts ?? [];

    /// <summary>
    /// Has ABOM print a query's tree over its test database, keeping who asked; a refusal names
    /// what stands in the way, such as a database older than its fixtures.
    /// </summary>
    public Task<(QueryCatalog? Catalog, string? Refusal)> PrintQueryAstAsync(
        string queryId, string queryVersion, string by, CancellationToken ct = default) =>
        ControlAsync<QueryCatalog>("api/queries/ast", new { queryId, queryVersion, by }, ct);

    /// <summary>What a request's target last reported, for reading against the request's examples.</summary>
    public async Task<ReviewState?> GetReviewAsync(long request, CancellationToken ct = default) =>
        await GetAsync<ReviewState>($"api/query-requests/{request}/review", ct);

    /// <summary>
    /// Approves or rejects the report that was read. An approval makes it the expected result and
    /// ABOM then runs the tests; a refusal says what stood in the way.
    /// </summary>
    public Task<(ReviewState? State, string? Refusal)> ReviewAsync(
        long request, string verdict, string reason, string by, string digest, CancellationToken ct = default) =>
        ControlAsync<ReviewState>($"api/query-requests/{request}/review", new { verdict, reason, by, digest }, ct);

    /// <summary>The sessions drafting a request from a run's picked results, each with its newest draft.</summary>
    public async Task<DraftSessionList?> GetDraftsAsync(long run, CancellationToken ct = default) =>
        await GetAsync<DraftSessionList>($"api/runs/{run}/drafts", ct);

    /// <summary>Starts an agent drafting a request from what was picked; nothing is filed by it.</summary>
    public Task<(AuthoringSession? Session, string? Refusal)> StartDraftAsync(
        long run, string queryId, IReadOnlyList<RequestEvidence> evidence, string by, CancellationToken ct = default) =>
        ControlAsync<AuthoringSession>($"api/runs/{run}/drafts", new { queryId, by, evidence }, ct);

    /// <summary>Stops the session drafting from a run.</summary>
    public Task<(DraftSessionList? List, string? Refusal)> StopDraftAsync(long run, CancellationToken ct = default) =>
        ControlAsync<DraftSessionList>($"api/runs/{run}/drafts/stop", null, ct);

    /// <summary>A guarded POST whose refusal is the reader's to read.</summary>
    private async Task<(T? Answer, string? Refusal)> ControlAsync<T>(string path, object? body, CancellationToken ct)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, path);
        request.Headers.Add("X-Abom-Control", "1");
        if (body is not null)
        {
            request.Content = JsonContent.Create(body);
        }
        try
        {
            using var response = await http.SendAsync(request, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<T>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (default, refusal?.Error ?? $"the server refused it ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{path}: {ex.Message}";
            return (default, ex.Message);
        }
    }

    /// <summary>What one member of a run reported for one query, so results can be picked by name.</summary>
    public async Task<MemberFindings?> GetRunFindingsAsync(
        long id, string repository, string queryId, CancellationToken ct = default) =>
        await GetAsync<MemberFindings>(
            $"api/runs/{id}/results?repository={Uri.EscapeDataString(repository)}" +
            $"&query={Uri.EscapeDataString(queryId)}", ct);

    /// <summary>Whether a run of these versions should store findings, as the server suggests it.</summary>
    public async Task<FindingsSuggestion?> GetFindingsSuggestionAsync(
        IReadOnlyList<AnalysisQueryRef> queries, CancellationToken ct = default) =>
        queries.Count == 0
            ? null
            : await GetAsync<FindingsSuggestion>("api/runs/suggestion?" + string.Join("&",
                queries.Select(q => "query=" + Uri.EscapeDataString(q.QueryId + "@" + q.QueryVersion))), ct);

    /// <summary>
    /// Makes findings of what a held run kept: the whole run with no repository, one member with no
    /// results named, or the results named in it.
    /// </summary>
    public Task<(Conversion? Conversion, string? Refusal)> ConvertHeldAsync(
        long run, string repository, IReadOnlyList<long> results, string by, CancellationToken ct = default) =>
        ControlAsync<Conversion>($"api/runs/{run}/convert", new { repository, results, by }, ct);

    /// <summary>A run's comparisons with the code scanning alerts, newest first and without members.</summary>
    public async Task<ComparisonList?> GetRunComparisonsAsync(long run, CancellationToken ct = default) =>
        await GetAsync<ComparisonList>($"api/runs/{run}/comparisons", ct);

    /// <summary>One comparison with every member, which is where what was not compared is named.</summary>
    public async Task<Comparison?> GetComparisonAsync(long id, CancellationToken ct = default) =>
        await GetAsync<Comparison>($"api/comparisons/{id}", ct);

    /// <summary>Asks for a run's results to be set against the alerts GitHub holds now.</summary>
    public Task<(Comparison? Comparison, string? Refusal)> StartComparisonAsync(
        long run, string by, CancellationToken ct = default) =>
        ControlAsync<Comparison>($"api/runs/{run}/comparisons", new { by }, ct);

    public async Task<FindingTrace?> GetFindingTraceAsync(long id, CancellationToken ct = default) =>
        await GetAsync<FindingTrace>($"api/findings/{id}", ct);

    /// <summary>Carries out a recorded run, answering as soon as it has started.</summary>
    public async Task<(AnalysisRun? Run, string? Refusal)> ExecuteAnalysisRunAsync(
        long id, CancellationToken ct = default)
    {
        try
        {
            using var response = await http.PostAsync($"api/runs/{id}/start", content: null, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<AnalysisRun>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (null, refusal?.Error
                ?? $"the server refused to start this run ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"api/runs/{id}/start: {ex.Message}";
            return (null, null);
        }
    }

    /// <summary>
    /// Records that somebody moved a version to a stage, answering the catalog the move produced.
    ///
    /// The refusal text is returned rather than thrown: a stage the server will not accept is the
    /// reader having asked for something, not ABOM being broken.
    /// </summary>
    public async Task<(QueryCatalog? Catalog, string? Refusal)> AdvanceQueryStageAsync(
        string queryId, string version, string stage, string by, CancellationToken ct = default)
    {
        try
        {
            using var response = await http.PostAsJsonAsync("api/queries/stage",
                new { queryId, queryVersion = version, stage, by }, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<QueryCatalog>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (null, refusal?.Error
                ?? $"the server refused this advance ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"api/queries/stage: {ex.Message}";
            return (null, null);
        }
    }

    /// <summary>
    /// The values one term can take, read from the endpoint the catalog named rather than from a
    /// URL built here: which terms have corpus-derived values is the catalog's to say.
    /// </summary>
    public async Task<FilterValueSet> GetFilterValuesAsync(
        string endpoint, CancellationToken ct = default) =>
        await GetAsync<FilterValueSet>(endpoint, ct) ?? new FilterValueSet();

    /// <summary>
    /// Keeps the scope under a name, answering the catalog the save produced.
    /// </summary>
    ///
    /// The predicate is sent, never the name of the tab it came from: a view recording "whatever
    /// `owned` means" would change meaning when the built-in did. The presentation travels beside
    /// it, and a surface with nothing to draw sends none rather than an empty one.
    public async Task<IReadOnlyList<NamedView>?> SaveViewAsync(
        string name, string predicate, string? presentation = null,
        CancellationToken ct = default) =>
        await WriteViewAsync(HttpMethod.Put, name, predicate, presentation, ct);

    public async Task<IReadOnlyList<NamedView>?> ForgetViewAsync(
        string name, CancellationToken ct = default) =>
        await WriteViewAsync(HttpMethod.Delete, name, null, null, ct);

    private async Task<IReadOnlyList<NamedView>?> WriteViewAsync(
        HttpMethod method, string name, string? predicate, string? presentation,
        CancellationToken ct)
    {
        var url = $"api/views/{Uri.EscapeDataString(name)}";
        using var request = new HttpRequestMessage(method, url);
        if (predicate is not null)
        {
            request.Content = string.IsNullOrEmpty(presentation)
                ? JsonContent.Create(new { predicate })
                : JsonContent.Create(new { predicate, presentation });
        }
        request.Headers.Add("X-Abom-Control", "1");
        try
        {
            using var response = await http.SendAsync(request, ct);
            if (!response.IsSuccessStatusCode)
            {
                var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
                LastError = refusal?.Error ?? $"the server refused it ({(int)response.StatusCode})";
                return null;
            }
            LastError = null;
            return await response.Content.ReadFromJsonAsync<List<NamedView>>(Json, ct) ?? [];
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{url}: {ex.Message}";
            return null;
        }
    }

    /// <summary>
    /// One page of the repositories unit, ordered across the whole match by the server.
    /// </summary>
    public async Task<(RepositoriesPage? Page, string? Refusal)> GetRepositoriesPageAsync(
        string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct = default) =>
        await GetPageAsync<RepositoriesPage>("api/repositories", predicate, top, offset, sort, desc, ct);

    /// <summary>
    /// One page of the inventory, ordered across the whole match by the server.
    /// </summary>
    public async Task<(InventoryPage? Page, string? Refusal)> GetActionsPageAsync(
        string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct = default) =>
        await GetPageAsync<InventoryPage>("api/actions", predicate, top, offset, sort, desc, ct);

    /// <summary>One page of the workflows unit.</summary>
    public async Task<(WorkflowsPage? Page, string? Refusal)> GetWorkflowsPageAsync(
        string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct = default) =>
        await GetPageAsync<WorkflowsPage>("api/workflows", predicate, top, offset, sort, desc, ct);

    /// <summary>One page of the jobs unit.</summary>
    public async Task<(JobsPage? Page, string? Refusal)> GetJobsPageAsync(
        string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct = default) =>
        await GetPageAsync<JobsPage>("api/jobs", predicate, top, offset, sort, desc, ct);

    /// <summary>
    /// One page of a listing, with the server's refusal carried back verbatim.
    /// </summary>
    /// <remarks>
    /// The refusal is not swallowed: the endpoint names the terms its unit cannot answer, and that
    /// name is the only thing that tells a reader why their scope is not narrowing this screen.
    /// Sorting and paging are the server's, because ordering a page answers a different question
    /// from ordering the match.
    /// </remarks>
    private async Task<(T? Page, string? Refusal)> GetPageAsync<T>(
        string path, string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct)
    {
        var parts = new List<string> { $"top={top}", $"offset={offset}" };
        if (!string.IsNullOrEmpty(sort))
        {
            parts.Add($"sort={Uri.EscapeDataString(sort)}");
            if (desc) parts.Add("desc=true");
        }
        if (predicate.Length > 0) parts.Add(predicate);
        var url = path + "?" + string.Join("&", parts);

        try
        {
            using var response = await http.GetAsync(url, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<T>(Json, ct), null);
            }
            var refusal = await response.Content.ReadFromJsonAsync<ApiError>(Json, ct);
            return (default, refusal?.Error ?? $"the server refused this view ({(int)response.StatusCode})");
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{url}: {ex.Message}";
            return (default, null);
        }
    }

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

    /// <summary>Replaces the declaration wholesale with the intended final scope.</summary>
    public async Task<IReadOnlyList<DeclaredAccount>> SetOwnedScopeAsync(
        IReadOnlyList<DeclaredAccount> accounts, CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(HttpMethod.Put, "api/owned-scope")
        {
            Content = JsonContent.Create(accounts),
        };
        request.Headers.Add("X-Abom-Control", "1");
        using var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<IReadOnlyList<DeclaredAccount>>(Json, ct)
               ?? [];
    }

    /// <summary>Turns the declared account scope into repositories.</summary>
    public async Task<EnumerationReport?> EnumerateOwnedScopeAsync(CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, "api/owned-scope/enumerate");
        request.Headers.Add("X-Abom-Control", "1");
        using var response = await http.SendAsync(request, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<EnumerationReport>(Json, ct);
    }

    /// <summary>Asks what a candidate declaration would admit and drop, without writing.</summary>
    public async Task<ScopePreview?> PreviewOwnedScopeAsync(
        IReadOnlyList<DeclaredAccount> accounts, CancellationToken ct = default)
    {
        using var response = await http.PostAsJsonAsync("api/owned-scope/preview", accounts, ct);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<ScopePreview>(Json, ct);
    }

    /// <summary>Reads how one property key divides repositories under review.</summary>
    public async Task<PropertiesView?> GetPropertiesAsync(
        string? property = null, CancellationToken ct = default)
        => await GetAsync<PropertiesView>(
            string.IsNullOrEmpty(property)
                ? "api/properties"
                : "api/properties?property=" + Uri.EscapeDataString(property),
            ct);

    /// <summary>Builds a BOM download URL that preserves the visible scope.</summary>
    public static string BomDownloadUrl(string format, IEnumerable<string>? roots = null,
        IEnumerable<string>? properties = null, string? mode = null)
    {
        var parts = new List<string> { "format=" + Uri.EscapeDataString(format), "download=true" };
        parts.AddRange((roots ?? []).Select(r => "root=" + Uri.EscapeDataString(r)));
        parts.AddRange((properties ?? []).Select(p => "properties=" + Uri.EscapeDataString(p)));
        if (!string.IsNullOrEmpty(mode)) parts.Add("mode=" + Uri.EscapeDataString(mode));
        return "api/bom?" + string.Join("&", parts);
    }

    // Reference may be empty, so it belongs in the query string.
    public async Task<NodeUsers?> GetNodeUsersAsync(
        string key, string reference, CancellationToken ct = default) =>
        await GetAsync<NodeUsers>(
            $"api/node/{key}/-/users?ref={Uri.EscapeDataString(reference)}", ct);

    public async Task<IReadOnlyList<FrontierItem>> GetFrontierAsync(
        string? status = null, int top = 500, CancellationToken ct = default) =>
        await GetListAsync<FrontierItem>(
            $"api/frontier?top={top}&status={Uri.EscapeDataString(status ?? string.Empty)}", ct);

    private async Task<T?> GetAsync<T>(string url, CancellationToken ct)
    {
        var started = Stopwatch.GetTimestamp();
        try
        {
            var result = await http.GetFromJsonAsync<T>(url, Json, ct);
            LastError = null;
            Record(url, started, ok: true);
            return result;
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{url}: {ex.Message}";
            // Failed fetches still count as waits.
            Record(url, started, ok: false);
            return default;
        }
    }

    private void Record(string url, long started, bool ok)
    {
        var elapsed = Stopwatch.GetElapsedTime(started);
        _recent.Add(new FetchTiming(url, (long)elapsed.TotalMilliseconds, ok));
        // Bound polling history.
        if (_recent.Count > RecentFetchLimit)
        {
            _recent.RemoveRange(0, _recent.Count - RecentFetchLimit);
        }
    }

    private async Task<IReadOnlyList<T>> GetListAsync<T>(string url, CancellationToken ct) =>
        await GetAsync<List<T>>(url, ct) ?? [];

    /// <summary>The most recent transport failure, surfaced rather than swallowed.</summary>
    public string? LastError { get; private set; }

    /// <summary>How many recent fetches are kept.</summary>
    public const int RecentFetchLimit = 64;

    private readonly List<FetchTiming> _recent = [];

    /// <summary>Browser-observed API waits, oldest first.</summary>
    public IReadOnlyList<FetchTiming> RecentFetches => _recent;
}

/// <summary>One fetch, as the browser experienced it.</summary>
/// <param name="Url">The endpoint asked for, relative to the API root.</param>
/// <param name="Milliseconds">Wall time from request to parsed result.</param>
/// <param name="Ok">False when the fetch failed, which is still a wait worth seeing.</param>
public readonly record struct FetchTiming(string Url, long Milliseconds, bool Ok);
