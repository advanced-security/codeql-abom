using System.Diagnostics;
using System.Net.Http.Json;
using System.Text.Json;
using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// Reads the ABOM over the API served by `abom serve`.
///
/// Responses are not cached: the database changes while an expansion runs, and a
/// stale coverage figure would misreport how much is known.
/// </summary>
public sealed class AbomApi(HttpClient http, TutorialService? tutorial = null)
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
        using var response = await SendControlRequestAsync(HttpMethod.Post, "api/accounts/refresh", null, ct);
        await ThrowUnlessSucceededAsync(response, ct);
        return await response.Content.ReadFromJsonAsync<AccountIdentity>(Json, ct)
               ?? new AccountIdentity();
    }

    /// <summary>
    /// Asks GitHub, through the operator's own gh token, which repositories an account holds.
    /// A guarded POST because it runs a subprocess on that token and names private repositories.
    /// </summary>
    public async Task<AccountRepositories> ListAccountRepositoriesAsync(
        string owner, CancellationToken ct = default) =>
        await SendControlAsync<AccountRepositories>(HttpMethod.Post,
            $"api/accounts/{Uri.EscapeDataString(owner)}/repositories", null, ct)
        ?? new AccountRepositories { Owner = owner };

    /// <summary>
    /// Reads an organization's custom properties for a property scope through the operator's gh
    /// token, with how many repositories carry each of <paramref name="clauses"/> now. Guarded like
    /// the repository listing, for the same reasons.
    /// </summary>
    public async Task<AccountProperties> ReadAccountPropertiesAsync(
        string owner, IReadOnlyList<IReadOnlyList<PropertyTerm>> clauses, CancellationToken ct = default) =>
        await SendControlAsync<AccountProperties>(HttpMethod.Post,
            $"api/accounts/{Uri.EscapeDataString(owner)}/properties", new { clauses }, ct)
        ?? new AccountProperties { Owner = owner };

    public async Task<IReadOnlyList<OwnerCandidate>> GetOwnerCandidatesAsync(
        CancellationToken ct = default) =>
        await GetListAsync<OwnerCandidate>("api/owner-candidates", ct);

    /// <summary>What this server is: an ordinary instance, or a Demo over practice data.</summary>
    public async Task<InstanceInfo?> GetInstanceAsync(CancellationToken ct = default) =>
        await GetAsync<InstanceInfo>("api/instance", ct);

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
    public async Task<DiscoveryState> SetDiscoveryAsync(bool running, CancellationToken ct = default) =>
        await SendControlAsync<DiscoveryState>(HttpMethod.Post,
            running ? "api/discovery/start" : "api/discovery/pause", null, ct) ?? new DiscoveryState();

    /// <summary>
    /// Records whether a server start may resume a crawl that was running. Guarded like the
    /// start control, because it decides what a future start spends with nobody present.
    /// </summary>
    public async Task<DiscoveryState> SetResumeOnStartAsync(
        bool resume, CancellationToken ct = default) =>
        await SendControlAsync<DiscoveryState>(HttpMethod.Post,
            "api/discovery/resume-on-start", new { resume }, ct) ?? new DiscoveryState();

    /// <summary>
    /// Runs a version's tests and records the verdict. ABOM never forms the opinion itself:
    /// `codeql test run` compares the query's output to the checked-in `.expected`.
    /// </summary>
    public Task<(QueryCatalog? Catalog, string? Refusal)> TestQueryAsync(
        string queryId, string version, CancellationToken ct = default) =>
        ControlAsync<QueryCatalog>(HttpMethod.Post, "api/queries/test",
            new { queryId, queryVersion = version }, ct, "this test run");

    /// <summary>
    /// Stops a run that is under way. What has already been analyzed is kept: the executor
    /// observes cancellation between members rather than discarding the work behind it.
    /// </summary>
    public Task<(AnalysisRun? Run, string? Refusal)> CancelAnalysisRunAsync(
        long id, CancellationToken ct = default) =>
        ControlAsync<AnalysisRun>(HttpMethod.Post, $"api/runs/{id}/cancel", null, ct, "to stop this run");

    /// <summary>What each dependency tier runs, and every suite it could be bound to.</summary>
    public async Task<SuiteBinding?> GetTierSuitesAsync(CancellationToken ct = default) =>
        await GetAsync<SuiteBinding>("api/discovery/suites", ct);

    /// <summary>
    /// Rewrites what one tier runs. Takes effect on the next round rather than the current one,
    /// because a round's queries are resolved once before any repository is claimed.
    /// </summary>
    public Task<SuiteBinding?> SetTierSuitesAsync(
        string tier, IReadOnlyList<string> suites, CancellationToken ct = default) =>
        SendControlAsync<SuiteBinding>(HttpMethod.Put,
            $"api/discovery/suites/{Uri.EscapeDataString(tier)}", suites, ct);

    /// <summary>How many repositories each Actions code scanning class holds, without their rows.</summary>
    public async Task<CodeScanningReadings?> GetCodeScanningReadingsAsync(CancellationToken ct = default) =>
        await GetAsync<CodeScanningReadings>("api/code-scanning/readings?summary=true", ct);

    /// <summary>Whether this server writes default setup, and to what.</summary>
    public async Task<CodeScanningWriteMode?> GetCodeScanningWriteModeAsync(CancellationToken ct = default) =>
        await GetAsync<CodeScanningWriteMode>("api/code-scanning/writes", ct);

    public async Task<IReadOnlyList<SetupBatch>?> GetSetupBatchesAsync(CancellationToken ct = default) =>
        await GetAsync<List<SetupBatch>>("api/code-scanning/batches", ct);

    /// <summary>The ledger's newest steps, newest first, and how many there are in all.</summary>
    public async Task<SetupLedgerPage?> GetSetupLedgerAsync(int top, CancellationToken ct = default) =>
        await GetAsync<SetupLedgerPage>($"api/code-scanning/ledger?top={top}", ct);

    /// <summary>
    /// Sends one guarded write to the default setup endpoints — a preview, an approval, a batch or
    /// an action on one — and answers the server's refusal as a reason rather than an exception.
    /// </summary>
    public Task<(T? Answer, string? Refusal)> SendCodeScanningAsync<T>(
        string path, object? body, CancellationToken ct = default) where T : class =>
        ControlAsync<T>(HttpMethod.Post, path, body ?? new { }, ct);

    /// <summary>What each kind of work may spend, what it spent, and the freshness its rate buys.</summary>
    public async Task<PoolsReport?> GetPoolsAsync(CancellationToken ct = default) =>
        await GetAsync<PoolsReport>("api/pools", ct);

    /// <summary>
    /// Sets one pool's rate, or clears it with a null rate. Guarded like every write, and the
    /// server's refusal comes back as the reason rather than an exception.
    /// </summary>
    public Task<(PoolsReport? Report, string? Refusal)> SetPoolRateAsync(
        string pool, string unit, int? perHour, int burst, CancellationToken ct = default) =>
        ControlAsync<PoolsReport>(HttpMethod.Put, "api/pools",
            new { pool, unit, perHour, burst }, ct);

    /// <summary>
    /// Puts a whole dependency tier back in the queue, so a changed or extended query suite can
    /// be run over work already analyzed. Nothing already known is cleared.
    /// </summary>
    public async Task<DiscoveryState> RequeueTierAsync(string tier, CancellationToken ct = default) =>
        await SendControlAsync<DiscoveryState>(HttpMethod.Post,
            $"api/discovery/requeue/{Uri.EscapeDataString(tier)}", null, ct) ?? new DiscoveryState();

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
    /// Puts back exactly the Coverage rows chosen, ahead of the ranking, and has the next cycle read
    /// again each owned repository among them and list each chosen account whole. A refusal throws
    /// with the server's reason, which names what was wrong.
    /// </summary>
    public Task<(ResyncResult? Result, ApiProblem? Refusal)> ResyncAsync(
        IEnumerable<FrontierItem> rows, IEnumerable<string> accounts, CancellationToken ct = default) =>
        SendGuardedAsync<ResyncResult>(HttpMethod.Post, "api/discovery/resync",
            new { rows = rows.Select(r => new { nodeKey = r.NodeKey, @ref = r.Ref }), accounts }, ct);

    /// <summary>
    /// Puts back every dependency that could not be fetched. Nothing does so on its own, because
    /// only the operator knows that access changed.
    /// </summary>
    public Task<RequeueResult> RetryUnavailableAsync(CancellationToken ct = default) =>
        RequeueAsync("api/discovery/retry-unavailable", null, ct);

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

    private async Task<RequeueResult> RequeueAsync(string path, object? body, CancellationToken ct) =>
        await SendControlAsync<RequeueResult>(HttpMethod.Post, path, body, ct) ?? new RequeueResult();

    public async Task<GraphVersion> GetGraphVersionAsync(CancellationToken ct = default) =>
        await GetAsync<GraphVersion>("api/graph/version", ct) ?? new GraphVersion();

    /// <summary>What leaving the declared closure would remove. Changes nothing.</summary>
    public async Task<PruneView?> GetPruneAsync(CancellationToken ct = default) =>
        await GetAsync<PruneView>("api/prune", ct);

    /// <summary>
    /// Removes from view what a reviewed preview showed, naming it by its proof. The server applies
    /// it only while nothing that preview kept would go, and most of the graph only when
    /// <paramref name="majority"/> says so; otherwise nothing moves and the removal as it now
    /// stands comes back to review instead. Any other refusal throws, like every control.
    /// </summary>
    public async Task<PruneOutcome> ApplyPruneAsync(string proof, bool majority, CancellationToken ct = default)
    {
        using var response = await SendControlRequestAsync(HttpMethod.Post, "api/prune/apply", new { proof, majority }, ct);
        if (response.StatusCode == System.Net.HttpStatusCode.Conflict)
        {
            return new PruneOutcome(null, await response.Content.ReadFromJsonAsync<PruneConflict>(Json, ct) ?? new PruneConflict());
        }
        await ThrowUnlessSucceededAsync(response, ct);
        return new PruneOutcome(await response.Content.ReadFromJsonAsync<PruneView>(Json, ct), null);
    }

    /// <summary>Deletes what is already removed from view. Not reversible: only re-analysis rebuilds it.</summary>
    public async Task<PruneView?> CompactPruneAsync(CancellationToken ct = default)
    {
        using var response = await SendControlRequestAsync(HttpMethod.Post, "api/prune/compact", null, ct);
        await ThrowUnlessSucceededAsync(response, ct);
        return await response.Content.ReadFromJsonAsync<PruneView>(Json, ct);
    }

    /// <summary>A refusal in the server's words, which say what did or did not happen, not only its status.</summary>
    private static async Task ThrowUnlessSucceededAsync(HttpResponseMessage response, CancellationToken ct)
    {
        if (response.IsSuccessStatusCode)
        {
            return;
        }
        throw new ApiRefusal(await ProblemOfAsync(response, "it", ct));
    }

    public async Task<IReadOnlyList<RemediationItem>> GetRemediationAsync(
        int top = 50, CancellationToken ct = default) =>
        (await GetPageAsync<AdvisoriesPage>("api/remediation", "", top, 0, null, false, ct)).Page?.Items ?? [];

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
        var result = await ReadRefusableAsync<AnalysisRun>("api/runs",
            () => SendControlRequestAsync(HttpMethod.Post, "api/runs?" + predicate, new { queries, by, held }, ct),
            "this run", ct);
        WatchRun(result.Answer);
        return result;
    }

    private void WatchRun(AnalysisRun? run)
    {
        if (run is not { State: "pending" or "running" }) return;
        tutorial?.WatchWork($"run:{run.RowId}", "Finish or cancel the run on CodeQL / Runs before starting a tutorial.",
            async token => (await GetAnalysisRunAsync(run.RowId, token)) is { } current
                ? current.State is "pending" or "running" : null);
    }

    /// <summary>What has been asked for, newest first.</summary>
    public async Task<IReadOnlyList<AnalysisRun>> GetAnalysisRunsAsync(
        CancellationToken ct = default) =>
        (await GetAsync<AnalysisRunList>("api/runs", ct))?.Runs ?? [];

    /// <summary>
    /// What a run over <paramref name="predicate"/> would cover, before anything runs. The server's
    /// refusal comes back verbatim, since it names the term a run cannot apply.
    /// </summary>
    public Task<(DatabaseSet? Set, string? Refusal)> GetDatabasesAsync(
        string predicate, CancellationToken ct = default)
    {
        var url = "api/databases?" + predicate;
        return ReadRefusableAsync<DatabaseSet>(url, () => http.GetAsync(url, ct), "this scope", ct);
    }

    /// <summary>Every query request, newest first, with the vocabularies a form offers.</summary>
    public async Task<QueryRequestList> GetQueryRequestsAsync(CancellationToken ct = default) =>
        await GetAsync<QueryRequestList>("api/query-requests", ct) ?? new QueryRequestList();

    /// <summary>A refusal is the reader leaving something out, so it is returned, not thrown.</summary>
    public Task<(QueryRequestList? List, string? Refusal)> SaveQueryRequestAsync(
        QueryRequest request, CancellationToken ct = default)
    {
        var path = request.RowId == 0 ? "api/query-requests" : $"api/query-requests/{request.RowId}";
        return ReadRefusableAsync<QueryRequestList>(path,
            () => SendControlRequestAsync(request.RowId == 0 ? HttpMethod.Post : HttpMethod.Put, path, request, ct),
            "this request", ct);
    }

    /// <summary>
    /// Deletes a request with its sessions and their timelines; files a session wrote stay in the
    /// pack. A refusal says why, such as a session still working it.
    /// </summary>
    public Task<(QueryRequestList? List, string? Refusal)> DeleteQueryRequestAsync(
        long request, CancellationToken ct = default) =>
        ControlAsync<QueryRequestList>(HttpMethod.Delete, $"api/query-requests/{request}", null, ct);

    /// <summary>
    /// Whether an agent session could start and, for each thing it needs, why not. Fresh probes
    /// each CLI again rather than trusting what the server last read.
    /// </summary>
    public async Task<AuthoringReadiness?> GetAuthoringReadinessAsync(bool fresh = false, CancellationToken ct = default) =>
        await GetAsync<AuthoringReadiness>(fresh ? "api/authoring/readiness?fresh=1" : "api/authoring/readiness", ct);

    /// <summary>
    /// Names the executable a tool runs, checked by the server first; empty returns it to the
    /// flag or environment default.
    /// </summary>
    public Task<(AuthoringReadiness? Readiness, string? Refusal)> SetToolPathAsync(
        string tool, string path, string by, CancellationToken ct = default) =>
        ControlAsync<AuthoringReadiness>(HttpMethod.Put, $"api/authoring/tools/{Uri.EscapeDataString(tool)}",
            new { path, by }, ct);

    /// <summary>
    /// What a linked example reads as, read by the server from GitHub without keeping anything. A
    /// refusal says why the link cannot be read.
    /// </summary>
    public Task<(RequestExample? Example, string? Refusal)> ReadRequestExampleAsync(
        RequestExample example, CancellationToken ct = default) =>
        ControlAsync<RequestExample>("api/query-requests/example", example, ct);

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
    public async Task<(AuthoringSession? Session, string? Refusal)> StartAuthoringSessionAsync(
        long request, string by, CancellationToken ct = default)
    {
        var result = await ControlAsync<AuthoringSession>($"api/query-requests/{request}/sessions", new { by }, ct);
        if (result.Answer is { State: "running" } session)
            tutorial?.WatchWork($"session:{session.RowId}", "Finish or stop the authoring session on CodeQL / Requests before starting a tutorial.",
                async token => (await GetAuthoringSessionsAsync(request, token))?.Sessions
                    .FirstOrDefault(s => s.RowId == session.RowId) is { } current ? current.State == "running" : null);
        return result;
    }

    /// <summary>Stops the session working a request.</summary>
    public Task<(AuthoringSessionList? List, string? Refusal)> StopAuthoringSessionAsync(
        long request, CancellationToken ct = default) =>
        ControlAsync<AuthoringSessionList>($"api/query-requests/{request}/sessions/stop", null, ct);

    public async Task<AuthoringEventPage> GetAuditEventsAsync(string query, CancellationToken ct = default)
    {
        using var response = await http.GetAsync("api/authoring-events" + (query.Length > 0 ? "?" + query : ""), ct);
        // Both answer the filters themselves: a malformed term, or a session, request or run that is
        // not recorded. Neither is the server being unavailable, nor an empty log.
        if (response.StatusCode is System.Net.HttpStatusCode.BadRequest or System.Net.HttpStatusCode.NotFound)
        {
            var problem = await response.Content.ReadFromJsonAsync<ApiProblem>(Json, ct);
            throw new AuditFilterException(problem?.Detail is { Length: > 0 } detail ? detail : "The audit filters were rejected.");
        }
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadFromJsonAsync<AuthoringEventPage>(Json, ct)
            ?? throw new HttpRequestException("The audit response was empty.");
    }

    public async Task<AuditSession> GetAuditSessionAsync(long session, CancellationToken ct = default) =>
        await http.GetFromJsonAsync<AuditSession>($"api/authoring-sessions/{session}", Json, ct)
        ?? throw new HttpRequestException("The session response was empty.");

    public async Task<AuditSessionList> GetAuditSessionsAsync(string query, CancellationToken ct = default) =>
        await http.GetFromJsonAsync<AuditSessionList>($"api/authoring-sessions?{query}", Json, ct)
        ?? throw new HttpRequestException("The session response was empty.");

    /// <summary>The trees a request's sessions printed, without their bodies.</summary>
    public async Task<IReadOnlyList<AuthoringAst>> GetAuthoringAstsAsync(long request, CancellationToken ct = default) =>
        (await GetAsync<AuthoringAstList>($"api/query-requests/{request}/asts", ct))?.Asts ?? [];

    /// <summary>One printed tree, body and all.</summary>
    public async Task<AuthoringAst?> GetAuthoringAstAsync(long id, CancellationToken ct = default) =>
        await GetAsync<AuthoringAst>($"api/authoring-asts/{id}", ct);

    /// <summary>The trees printed for a query, by a session or by ABOM, without their bodies.</summary>
    public async Task<IReadOnlyList<AuthoringAst>> GetQueryAstsAsync(string queryId, CancellationToken ct = default) =>
        (await GetAsync<AuthoringAstList>($"api/queries/asts?query={Uri.EscapeDataString(queryId)}", ct))?.Asts ?? [];

    /// <summary>Everything that makes a query on disk run, and its newest test run, or why not.</summary>
    public Task<(QueryComponentsShown? Shown, ApiProblem? Refusal)> GetQueryComponentsAsync(
        string queryId, CancellationToken ct = default) =>
        ReadGuardedAsync<QueryComponentsShown>($"api/queries/components?query={Uri.EscapeDataString(queryId)}", ct);

    /// <summary>One component of a query, as it is on disk, or why it cannot be read.</summary>
    public Task<(StepFile? File, ApiProblem? Refusal)> GetQueryComponentAsync(
        string queryId, string path, CancellationToken ct = default) =>
        ReadGuardedAsync<StepFile>(
            $"api/queries/component?query={Uri.EscapeDataString(queryId)}&path={Uri.EscapeDataString(path)}", ct);

    /// <summary>
    /// Opens a pull request of a query in the publisher App's one repository; a refusal names what
    /// stands in the way, such as a publisher installed more broadly than that.
    /// </summary>
    public Task<(QueryExport? Export, ApiProblem? Refusal)> ExportQueryAsync(string queryId, string bundleSha256,
        CancellationToken ct = default) =>
        SendGuardedAsync<QueryExport>(HttpMethod.Post,
            $"api/queries/export?query={Uri.EscapeDataString(queryId)}&bundle={Uri.EscapeDataString(bundleSha256)}",
            null, ct, "the export");

    /// <summary>Where a query's bundle downloads from.</summary>
    public static string QueryBundleUrl(string queryId) => $"api/queries/bundle?query={Uri.EscapeDataString(queryId)}";

    /// <summary>The files a step of a query on disk was read from, as they are now.</summary>
    public async Task<StepShown?> GetQueryStepAsync(string queryId, string step, CancellationToken ct = default) =>
        await GetAsync<StepShown>(
            $"api/queries/step?query={Uri.EscapeDataString(queryId)}&step={Uri.EscapeDataString(step)}", ct);

    /// <summary>
    /// Has ABOM print a query's tree over its test database, keeping who asked; a refusal names
    /// what stands in the way, such as a database older than its fixtures.
    /// </summary>
    public Task<(QueryCatalog? Catalog, string? Refusal)> PrintQueryAstAsync(
        string queryId, string queryVersion, string by, CancellationToken ct = default) =>
        ControlAsync<QueryCatalog>("api/queries/ast", new { queryId, queryVersion, by }, ct);

    /// <summary>The sessions drafting a request from a run's picked results, each with its newest draft.</summary>
    public async Task<DraftSessionList?> GetDraftsAsync(long run, CancellationToken ct = default) =>
        await GetAsync<DraftSessionList>($"api/runs/{run}/drafts", ct);

    /// <summary>Starts an agent drafting a request from what was picked; nothing is filed by it.</summary>
    public async Task<(AuthoringSession? Session, string? Refusal)> StartDraftAsync(
        long run, string queryId, IReadOnlyList<RequestEvidence> evidence, string by, CancellationToken ct = default)
    {
        var result = await ControlAsync<AuthoringSession>($"api/runs/{run}/drafts", new { queryId, by, evidence }, ct);
        if (result.Answer is { State: "running" } session)
            tutorial?.WatchWork($"session:{session.RowId}", "Finish or stop the request draft before starting a tutorial.",
                async token => (await GetDraftsAsync(run, token))?.Drafts
                    .FirstOrDefault(s => s.RowId == session.RowId) is { } current ? current.State == "running" : null);
        return result;
    }

    /// <summary>Stops the session drafting from a run.</summary>
    public Task<(DraftSessionList? List, string? Refusal)> StopDraftAsync(long run, CancellationToken ct = default) =>
        ControlAsync<DraftSessionList>($"api/runs/{run}/drafts/stop", null, ct);

    /// <summary>A guarded POST whose refusal is the reader's to read.</summary>
    private Task<(T? Answer, string? Refusal)> ControlAsync<T>(string path, object? body, CancellationToken ct) =>
        ControlAsync<T>(HttpMethod.Post, path, body, ct);

    private async Task<(T? Answer, string? Refusal)> ControlAsync<T>(
        HttpMethod method, string path, object? body, CancellationToken ct, string refused = "it")
    {
        var (answer, problem) = await SendGuardedAsync<T>(method, path, body, ct, refused);
        return (answer, problem is { Code: ApiProblem.UnreachableCode } ? $"{problem.Title}: {problem.Detail}" : problem?.Detail);
    }

    /// <summary>
    /// A control, answered or refused: the refusal is the server's problem detail, whose code says
    /// which kind it is, so a page can say what to do next rather than only that it failed. A
    /// transport failure is a problem too, with no code the server sent.
    /// </summary>
    public async Task<(T? Answer, ApiProblem? Refusal)> SendGuardedAsync<T>(
        HttpMethod method, string path, object? body, CancellationToken ct, string refused = "it")
    {
        using var request = ControlRequest(method, path, body);
        try
        {
            using var response = await http.SendAsync(request, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<T>(Json, ct), null);
            }
            return (default, await ProblemOfAsync(response, refused, ct));
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{path}: {ex.Message}";
            return (default, ApiProblem.Unreachable(ex.Message));
        }
    }

    /// <summary>A read whose refusal is the reader's to see, in the server's words and code.</summary>
    public async Task<(T? Answer, ApiProblem? Refusal)> ReadGuardedAsync<T>(string url, CancellationToken ct)
    {
        var started = Stopwatch.GetTimestamp();
        try
        {
            using var response = await http.GetAsync(url, ct);
            Record(url, started, ok: response.IsSuccessStatusCode);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<T>(Json, ct), null);
            }
            return (default, await ProblemOfAsync(response, "this read", ct));
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{url}: {ex.Message}";
            Record(url, started, ok: false);
            return (default, ApiProblem.Unreachable(ex.Message));
        }
    }

    /// <summary>
    /// The one reading of a refusal: the server's problem detail, or, where the body is not one, the
    /// status alone, said as such.
    /// </summary>
    private static async Task<ApiProblem> ProblemOfAsync(HttpResponseMessage response, string refused, CancellationToken ct)
    {
        var status = (int)response.StatusCode;
        var body = await response.Content.ReadAsStringAsync(ct);
        try
        {
            if (JsonSerializer.Deserialize<ApiProblem>(body, Json) is { Detail.Length: > 0 } problem)
            {
                return problem;
            }
        }
        catch (JsonException)
        {
            // Not the server's shape: a proxy's page, or nothing at all, which still has its status.
        }
        return new ApiProblem { Status = status, Detail = $"the server refused {refused} ({status})" };
    }

    private static HttpRequestMessage ControlRequest(HttpMethod method, string path, object? body)
    {
        var request = new HttpRequestMessage(method, path);
        request.Headers.Add("X-Abom-Control", "1");
        if (body is not null) request.Content = JsonContent.Create(body);
        return request;
    }

    private async Task<T?> SendControlAsync<T>(
        HttpMethod method, string path, object? body, CancellationToken ct)
    {
        using var response = await SendControlRequestAsync(method, path, body, ct);
        await ThrowUnlessSucceededAsync(response, ct);
        return await response.Content.ReadFromJsonAsync<T>(Json, ct);
    }

    private async Task<HttpResponseMessage> SendControlRequestAsync(
        HttpMethod method, string path, object? body, CancellationToken ct)
    {
        using var request = ControlRequest(method, path, body);
        return await http.SendAsync(request, ct);
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
    public async Task<(Comparison? Comparison, string? Refusal)> StartComparisonAsync(
        long run, string by, CancellationToken ct = default)
    {
        var result = await ControlAsync<Comparison>($"api/runs/{run}/comparisons", new { by }, ct);
        if (result.Answer is { State: "running" or "pending" } comparison)
            tutorial?.WatchWork($"comparison:{comparison.RowId}", "Wait for the comparison on CodeQL / Runs before starting a tutorial.",
                async token => (await GetComparisonAsync(comparison.RowId, token)) is { } current
                    ? current.State is "pending" or "running" : null);
        return result;
    }

    public async Task<FindingTrace?> GetFindingTraceAsync(long id, CancellationToken ct = default) =>
        await GetAsync<FindingTrace>($"api/findings/{id}", ct);

    /// <summary>Carries out a recorded run, answering as soon as it has started.</summary>
    public async Task<(AnalysisRun? Run, string? Refusal)> ExecuteAnalysisRunAsync(
        long id, CancellationToken ct = default)
    {
        var result = await ReadRefusableAsync<AnalysisRun>($"api/runs/{id}/start",
            () => SendControlRequestAsync(HttpMethod.Post, $"api/runs/{id}/start", null, ct), "to start this run", ct);
        WatchRun(result.Answer);
        return result;
    }

    /// <summary>
    /// Records that somebody moved a version to a stage, answering the catalog the move produced.
    ///
    /// The refusal text is returned rather than thrown: a stage the server will not accept is the
    /// reader having asked for something, not ABOM being broken.
    /// </summary>
    public Task<(QueryCatalog? Catalog, string? Refusal)> AdvanceQueryStageAsync(
        string queryId, string version, string stage, string by, CancellationToken ct = default) =>
        ReadRefusableAsync<QueryCatalog>("api/queries/stage",
            () => SendControlRequestAsync(HttpMethod.Post, "api/queries/stage",
                new { queryId, queryVersion = version, stage, by }, ct),
            "this advance", ct);

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
                LastError = (await ProblemOfAsync(response, "it", ct)).Detail;
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

    public async Task<(FindingsPage? Page, string? Refusal)> GetFindingsPageAsync(
        string predicate, int top, int offset, string asset = "", string assetKind = "",
        string text = "", CancellationToken ct = default) =>
        await GetPageAsync<FindingsPage>("api/findings", ReviewPredicate(predicate, asset, assetKind, text),
            top, offset, null, false, ct);

    public async Task<(FindingDetails? Details, string? Refusal)> GetFindingReviewAsync(
        long id, string predicate, string asset = "", string assetKind = "",
        long evaluation = 0, CancellationToken ct = default) =>
        await GetPageAsync<FindingDetails>($"api/findings/{id}/review",
            ReviewPredicate(predicate, asset, assetKind, "") + (evaluation > 0 ? $"&evaluation={evaluation}" : ""),
            1, 0, null, false, ct);

    private static string ReviewPredicate(string predicate, string asset, string assetKind, string text)
    {
        var parts = new List<string>();
        if (predicate.Length > 0) parts.Add(predicate);
        if (asset.Length > 0)
        {
            parts.Add($"asset={Uri.EscapeDataString(asset)}");
            parts.Add($"assetKind={Uri.EscapeDataString(assetKind)}");
        }
        if (text.Length > 0) parts.Add($"text={Uri.EscapeDataString(text)}");
        return string.Join("&", parts);
    }

    /// <summary>
    /// One page of a listing, with the server's refusal carried back verbatim.
    /// </summary>
    /// <remarks>
    /// The refusal is not swallowed: the endpoint names the terms its unit cannot answer, and that
    /// name is the only thing that tells a reader why their scope is not narrowing this screen.
    /// Sorting and paging are the server's, because ordering a page answers a different question
    /// from ordering the match.
    /// </remarks>
    public Task<(T? Page, string? Refusal)> GetPageAsync<T>(
        string path, string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct = default)
    {
        var parts = new List<string> { $"top={top}", $"offset={offset}" };
        if (!string.IsNullOrEmpty(sort))
        {
            parts.Add($"sort={Uri.EscapeDataString(sort)}");
            if (desc) parts.Add("desc=true");
        }
        if (predicate.Length > 0) parts.Add(predicate);
        var url = path + "?" + string.Join("&", parts);
        return ReadRefusableAsync<T>(url, () => http.GetAsync(url, ct), "this view", ct);
    }

    private async Task<(T? Answer, string? Refusal)> ReadRefusableAsync<T>(
        string path, Func<Task<HttpResponseMessage>> send, string refused, CancellationToken ct)
    {
        try
        {
            using var response = await send();
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<T>(Json, ct), null);
            }
            return (default, (await ProblemOfAsync(response, refused, ct)).Detail);
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{path}: {ex.Message}";
            return (default, null);
        }
    }

    // The key contains slashes of its own, so it is appended as a path rather than escaped. `at`
    // is the predicate's references alone, the one term a node's own details apply.
    public async Task<NodeDetail?> GetNodeAsync(string key, string at = "", CancellationToken ct = default) =>
        await GetAsync<NodeDetail>($"api/node/{key}" + (at.Length > 0 ? "?" + at : ""), ct);

    /// <summary>One page of who uses a node under a scope, ordered across the whole match.</summary>
    public async Task<(NodeUsesPage? Page, string? Refusal)> GetNodeUsesAsync(
        string key, string predicate, int top, int offset, string? sort, bool desc,
        CancellationToken ct = default) =>
        await GetPageAsync<NodeUsesPage>($"api/node/{key}/-/uses", predicate, top, offset, sort, desc, ct);

    /// <summary>
    /// A node's figure under the page's terms. A refusal is the server's own sentence; a fault is a
    /// server that cannot draw, which is not the reader's to fix.
    /// </summary>
    public async Task<(NodeFigure? Figure, string? Problem, bool Fault)> GetNodeFigureAsync(
        string key, string predicate, CancellationToken ct = default)
    {
        var url = FigurePath(key, predicate, svg: false);
        try
        {
            using var response = await http.GetAsync(url, ct);
            if (response.IsSuccessStatusCode)
            {
                LastError = null;
                return (await response.Content.ReadFromJsonAsync<NodeFigure>(Json, ct), null, false);
            }
            var fault = (int)response.StatusCode >= 500;
            return (null, (await ProblemOfAsync(response, "the figure", ct)).Detail, fault);
        }
        catch (Exception ex) when (ex is HttpRequestException or JsonException or TaskCanceledException)
        {
            LastError = $"{url}: {ex.Message}";
            return (null, $"the figure could not be read: {ex.Message}", true);
        }
    }

    /// <summary>Where a node's figure is, as data or as the file to download.</summary>
    public static string FigurePath(string key, string predicate, bool svg) =>
        $"api/node/{key}/-/closure{(svg ? ".svg" : "")}" + (predicate.Length > 0 ? "?" + predicate : "");

    /// <summary>Every reference a node is used at, counted under a scope.</summary>
    public async Task<(NodeVersionsPage? Page, string? Refusal)> GetNodeVersionsAsync(
        string key, string predicate, CancellationToken ct = default) =>
        await GetPageAsync<NodeVersionsPage>($"api/node/{key}/-/versions", predicate, 0, 0, null, false, ct);

    public async Task<IReadOnlyList<DeclaredAccount>> GetOwnedScopeAsync(
        CancellationToken ct = default) =>
        await GetListAsync<DeclaredAccount>("api/owned-scope", ct);

    /// <summary>Who is asking and what they may do, or null when it could not be read.</summary>
    public async Task<Me?> ReadMeAsync(CancellationToken ct = default) =>
        await GetAsync<Me>("api/me", ct);

    /// <summary>
    /// Ends this browser's session, answering whether it ended. The identity provider's own session
    /// is its to end.
    /// </summary>
    public async Task<bool> SignOutAsync(CancellationToken ct = default)
    {
        try
        {
            using var response = await http.PostAsync("auth/logout", null, ct);
            return response.IsSuccessStatusCode;
        }
        catch (HttpRequestException)
        {
            return false;
        }
    }

    /// <summary>The declaration's decision, or null when it could not be read, which says nothing either way.</summary>
    public async Task<ScopeDecision?> ReadScopeDecisionAsync(CancellationToken ct = default) =>
        await GetAsync<ScopeDecision>("api/owned-scope/decision", ct);

    /// <summary>
    /// The declaration, or null when it could not be read. <see cref="GetOwnedScopeAsync"/> answers
    /// a failed read with an empty list, which a page would report as nothing declared.
    /// </summary>
    public async Task<IReadOnlyList<DeclaredAccount>?> ReadOwnedScopeAsync(
        CancellationToken ct = default) =>
        await GetAsync<List<DeclaredAccount>>("api/owned-scope", ct);

    /// <summary>
    /// Replaces the declaration wholesale with the intended final scope. A refusal throws with the
    /// server's reason, which names what to change; a status code alone would not.
    /// </summary>
    public async Task<IReadOnlyList<DeclaredAccount>> SetOwnedScopeAsync(
        IReadOnlyList<DeclaredAccount> accounts, CancellationToken ct = default)
    {
        using var request = new HttpRequestMessage(HttpMethod.Put, "api/owned-scope")
        {
            Content = JsonContent.Create(accounts),
        };
        request.Headers.Add("X-Abom-Control", "1");
        using var response = await http.SendAsync(request, ct);
        if (!response.IsSuccessStatusCode)
        {
            throw new ApiRefusal(await ProblemOfAsync(response, "the declaration", ct));
        }
        return await response.Content.ReadFromJsonAsync<IReadOnlyList<DeclaredAccount>>(Json, ct)
               ?? [];
    }

    /// <summary>Turns the declared account scope into repositories.</summary>
    public Task<EnumerationReport?> EnumerateOwnedScopeAsync(CancellationToken ct = default) =>
        SendControlAsync<EnumerationReport>(HttpMethod.Post, "api/owned-scope/enumerate", null, ct);

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

    /// <summary>What a bill of materials rooted at one repository claims about itself.</summary>
    public async Task<BomSummary?> GetBomSummaryAsync(string root, CancellationToken ct = default) =>
        await GetAsync<BomSummary>($"api/bom?root={Uri.EscapeDataString(root)}&format=summary", ct);

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
