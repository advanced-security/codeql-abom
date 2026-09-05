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
        Services.AddSingleton<FollowService>();
    }

    /// <summary>Serves this JSON for any request whose path contains <paramref name="path"/>.</summary>
    ///
    /// Faked at the transport rather than by replacing AbomApi, so the JSON contract the server
    /// actually publishes stays under test.
    protected void Answer(string path, string json) => _handler.Answers[path] = json;

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
        public Dictionary<string, string> Answers { get; } = [];
        public Dictionary<string, TaskCompletionSource> Gates { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request, CancellationToken cancellationToken)
        {
            var url = request.RequestUri?.ToString() ?? string.Empty;
            foreach (var (path, gate) in Gates)
            {
                if (url.Contains(path, StringComparison.Ordinal))
                {
                    await gate.Task.WaitAsync(cancellationToken);
                }
            }
            foreach (var (path, json) in Answers)
            {
                if (url.Contains(path, StringComparison.Ordinal))
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
    }
}
