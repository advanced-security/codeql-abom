using System.Net;
using System.Net.Http;
using Abom.WebAssembly.Services;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The interface must be able to say whether it was waiting or drawing.
///
/// `ui/performance.md` reports 3.4 FPS at 9,811 nodes, and until now that named two suspects at
/// once: the API taking its time, and the browser taking its time with what arrived. `AbomApi`
/// timed nothing at all, so the split could not be made from the client side.
///
/// Server-side timing cannot close this. `/api/metrics` measures from the first byte of the
/// request to the last byte written, and knows nothing about transfer, deserialization, or the
/// render that follows.
/// </summary>
public sealed class ApiTimingTests
{
    [Fact]
    public async Task AFetchRecordsHowLongItTook()
    {
        using var http = new HttpClient(new SlowHandler(TimeSpan.FromMilliseconds(30)))
        {
            BaseAddress = new Uri("http://localhost/"),
        };
        var api = new AbomApi(http);

        await api.GetSummaryAsync(TestContext.Current.CancellationToken);

        var timings = api.RecentFetches;
        Assert.Single(timings);
        Assert.Equal("api/summary", timings[0].Url);
        Assert.True(timings[0].Milliseconds >= 20,
            $"a fetch stalled for 30 ms recorded {timings[0].Milliseconds} ms, which is not a "
            + "measurement of anything");
    }

    /// <summary>
    /// A failed fetch is still a wait, and the one worth seeing.
    ///
    /// Recording only successes would hide the case a reader complains about: the request that
    /// hung and then gave up.
    /// </summary>
    [Fact]
    public async Task AFailedFetchIsTimedToo()
    {
        using var http = new HttpClient(new FailingHandler())
        {
            BaseAddress = new Uri("http://localhost/"),
        };
        var api = new AbomApi(http);

        await api.GetSummaryAsync(TestContext.Current.CancellationToken);

        Assert.Single(api.RecentFetches);
        Assert.False(api.RecentFetches[0].Ok);
    }

    /// <summary>
    /// The ring is bounded, or an interface that polls forever becomes the leak it was added to
    /// find. The same rule the server's metrics follow.
    /// </summary>
    [Fact]
    public async Task TheFetchRingIsBounded()
    {
        using var http = new HttpClient(new SlowHandler(TimeSpan.Zero))
        {
            BaseAddress = new Uri("http://localhost/"),
        };
        var api = new AbomApi(http);

        for (var i = 0; i < AbomApi.RecentFetchLimit + 20; i++)
        {
            await api.GetSummaryAsync(TestContext.Current.CancellationToken);
        }

        Assert.Equal(AbomApi.RecentFetchLimit, api.RecentFetches.Count);
    }

    private sealed class SlowHandler(TimeSpan delay) : HttpMessageHandler
    {
        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request, CancellationToken cancellationToken)
        {
            if (delay > TimeSpan.Zero)
            {
                await Task.Delay(delay, cancellationToken);
            }
            return new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent("{}", System.Text.Encoding.UTF8, "application/json"),
            };
        }
    }

    private sealed class FailingHandler : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request, CancellationToken cancellationToken) =>
            throw new HttpRequestException("refused");
    }
}
