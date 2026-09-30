using System.Net;
using System.Text;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using MudBlazor;
using MudBlazor.Services;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The service set every component test needs, mirroring <c>Program.cs</c>.
///
/// Mirroring rather than sharing it: a test that registered its own convenient set would pass
/// against a container the application never builds.
/// </summary>
public abstract class AbomTestContext : BunitContext
{
    private readonly StubHandler _handler = new();

    protected AbomTestContext()
    {
        // MudBlazor reaches for the DOM on almost every component. Loose returns defaults rather
        // than throwing, which is what makes rendering possible outside a browser at all.
        JSInterop.Mode = JSRuntimeMode.Loose;

        // Tooltips and menus normally render into a popover root the real layout supplies.
        // MudPopoverProvider takes no child content, so it cannot wrap the component under test;
        // telling MudBlazor not to look for it is the supported way to render without a layout.
        Services.AddMudServices(options => options.PopoverOptions.CheckForPopoverProvider = false);
        Services.AddSingleton(new HttpClient(_handler) { BaseAddress = new Uri("http://localhost/") });
        Services.AddSingleton<AbomApi>();
        Services.AddSingleton<ThemeService>();
        Services.AddSingleton<AccountService>();
        Services.AddSingleton<ScopeService>();
        Services.AddSingleton<FollowService>();
        Services.AddSingleton<RunDraft>();
        Services.AddSingleton<RequestDraft>();

        _handler.Fallbacks.Add(("api/filters", DefaultFilters));
    }

    /// <summary>The filter catalog every surface's picker reads, unless a test answers its own.</summary>
    ///
    /// Registered late rather than in the constructor: <see cref="Answer"/> is first-registered-
    /// wins, so a test that cares about the catalog still overrides this by asking for it first.
    /// Without a default, every page carrying a picker renders no terms and no chips, and a test
    /// about something else fails for a reason that has nothing to do with it.
    private const string DefaultFilters = """
        [
          {"name":"scope","label":"Scope","help":"Owned accounts, or advisory routes",
           "arity":"scalar","values":["owned","exposure"],
           "surfaces":["graph","inventory","workflows","jobs","repositories","relationships"]},
          {"name":"q","label":"Name contains","help":"Repository, workflow or action",
           "arity":"scalar",
           "surfaces":["graph","inventory","workflows","jobs","repositories","relationships"]},
          {"name":"rules","label":"CodeQL rule","help":"Keep what a rule reported","arity":"list",
           "surfaces":["graph","inventory","relationships"]},
          {"name":"mode","label":"Analysis shape","help":"How the database was built",
           "arity":"scalar","values":["regular","extended"],
           "surfaces":["graph","inventory","relationships"]},
          {"name":"properties","label":"Repository Property","help":"Keep a property value",
           "arity":"property","valuesFrom":"api/properties",
           "surfaces":["graph","inventory","workflows","jobs","repositories","advisories","relationships","document"]}
        ]
        """;

    /// <summary>Serves this JSON for any request whose path contains <paramref name="path"/>.</summary>
    ///
    /// Faked at the transport rather than by replacing AbomApi, so the JSON contract the server
    /// actually publishes stays under test.
    ///
    /// <para>**First registered wins.** Paths overlap by design — <c>/-/users</c> and
    /// <c>api/node/</c> both match a users request, as do <c>api/owned-scope</c> and
    /// <c>api/owned-scope/preview</c> — so register the specific one first.</para>
    protected void Answer(string path, string json) => _handler.Answer(path, json);

    /// <summary>
    /// Puts a scope in the address, which the Explorer requires before it draws anything. A test
    /// about what a drawn graph does is not a test about how a reader came to choose it, and
    /// without this every one of them would be measuring the chooser instead.
    /// </summary>
    protected void ArriveWithAScope(string terms = "scope=owned&flags=findings") =>
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/explore?" + terms);

    /// <summary>
    /// Answers <paramref name="path"/> only where no test registered something for it. A fixture
    /// a whole class shares belongs here rather than in its constructor: <see cref="Answer"/> is
    /// first-registered-wins, so a shared answer set up in a constructor beats the narrower one a
    /// test registers for itself, and the test reads as though its own stub were ignored.
    /// </summary>
    protected void Fallback(string path, string json) => _handler.Fallbacks.Add((path, json));

    /// <summary>
    /// Answers requests for <paramref name="path"/> with <paramref name="status"/>.
    ///
    /// A refusal the server states deliberately arrives as a status code, and a page that
    /// handles it well is indistinguishable from one that never asked unless a test can make
    /// the call actually fail.
    /// </summary>
    protected void Fail(string path, HttpStatusCode status) => _handler.Fail(path, status);

    /// <summary>
    /// Every request the component made, in order.
    ///
    /// A page that renders a declaration correctly and writes the wrong one looks identical on
    /// screen, so what was sent has to be assertable separately from what was drawn.
    /// </summary>
    protected IReadOnlyList<SentRequest> Sent => _handler.Sent;

    protected sealed record SentRequest(string Method, string Url, string Body);

    /// <summary>
    /// Holds every answer for <paramref name="path"/> until the returned action is called.
    ///
    /// The interesting window is the one where a request is in flight: that is where a view can
    /// claim an answer it has not received, and it is invisible to a test whose stub replies
    /// instantly.
    /// </summary>
    protected Action Withhold(string path)
    {
        var gate = new TaskCompletionSource();
        _handler.Gates[path] = gate;
        return () => gate.TrySetResult();
    }

    private sealed class StubHandler : HttpMessageHandler
    {
        // Ordered, because which stub answers an overlapping path is a decision tests make by
        // registering the specific one first. A dictionary left that to its own ordering.
        private readonly List<(string Path, string Json)> _answers = [];
        private readonly List<(string Path, HttpStatusCode Status)> _failures = [];

        public Dictionary<string, TaskCompletionSource> Gates { get; } = [];
        public List<SentRequest> Sent { get; } = [];

        public void Fail(string path, HttpStatusCode status) => _failures.Add((path, status));

        /// <summary>Answers used only when no test registered one, so a default cannot win.</summary>
        public List<(string Path, string Json)> Fallbacks { get; } = [];

        public void Answer(string path, string json)
        {
            var at = _answers.FindIndex(a => a.Path == path);
            if (at >= 0)
            {
                _answers[at] = (path, json);
                return;
            }
            _answers.Add((path, json));
        }

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var url = request.RequestUri?.ToString() ?? string.Empty;
            var body = request.Content is null
                ? string.Empty
                : await request.Content.ReadAsStringAsync(cancellationToken);
            Sent.Add(new SentRequest(request.Method.Method, url, body));

            foreach (var (path, gate) in Gates)
            {
                if (url.Contains(path, StringComparison.Ordinal))
                {
                    await gate.Task.WaitAsync(cancellationToken);
                }
            }
            foreach (var (path, status) in _failures)
            {
                if (url.Contains(path, StringComparison.Ordinal))
                {
                    return new HttpResponseMessage(status)
                    {
                        Content = new StringContent($"{{\"error\":\"refused {path}\"}}",
                            Encoding.UTF8, "application/json"),
                    };
                }
            }
            foreach (var (path, json) in _answers.Concat(Fallbacks))
            {
                if (Matches(url, path))
                {
                    return new HttpResponseMessage(HttpStatusCode.OK)
                    {
                        Content = new StringContent(json, Encoding.UTF8, "application/json"),
                    };
                }
            }
            // An unstubbed call is a test asking for something it never said it wanted, and a
            // silent empty body would surface later as a puzzling assertion failure.
            return new HttpResponseMessage(HttpStatusCode.NotFound)
            {
                Content = new StringContent($"no stub for {url}"),
            };
        }

        /// <summary>
        /// Whether a registered path answers this URL. A deeper path is not the same endpoint:
        /// matching on substring alone had `api/filters` answering `api/filters/counts` with the
        /// whole catalog, which then failed to decode and read as "the counts did not load".
        /// </summary>
        private static bool Matches(string url, string path)
        {
            var at = url.IndexOf(path, StringComparison.Ordinal);
            if (at < 0) return false;
            var after = at + path.Length;
            return after >= url.Length || url[after] != '/';
        }
    }
}
