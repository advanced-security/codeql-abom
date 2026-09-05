using Abom.WebAssembly.Services;
using Abom.WebAssembly.Shared;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class FollowControlTests : AbomTestContext
{
    // A control offering to follow something that is not running is a control that does nothing,
    // and the reader is left to work out why.
    [Fact]
    public void FollowIsDisabledWhenNothingIsRunning()
    {
        Services.GetRequiredService<FollowService>().SetDiscoveryRunning(false);

        var cut = Render<FollowControl>();

        Assert.True(cut.Find("input[type=checkbox]").HasAttribute("disabled"));
    }

    // The interval is a preference, not a property of the current run: setting it before
    // starting anything is the normal case, so it stays settable when nothing is running.
    [Fact]
    public void TheIntervalStaysSettableWhileNothingIsRunning()
    {
        Services.GetRequiredService<FollowService>().SetDiscoveryRunning(false);

        var cut = Render<FollowControl>();

        Assert.DoesNotContain("disabled", cut.Find(".mud-select").OuterHtml);
    }

    // Stopping cannot leave the box ticked: it would claim to be following a run that ended.
    [Fact]
    public void StoppingClearsFollowing()
    {
        var follow = Services.GetRequiredService<FollowService>();
        follow.SetDiscoveryRunning(true);
        follow.SetEnabled(true);

        follow.SetDiscoveryRunning(false);

        Assert.False(follow.Enabled);
    }

    // Starting must not tick it on someone's behalf. Following pulls data every interval, and a
    // control that turns itself on is one nobody chose.
    [Fact]
    public void StartingDoesNotBeginFollowingOnItsOwn()
    {
        var follow = Services.GetRequiredService<FollowService>();
        follow.SetDiscoveryRunning(false);

        follow.SetDiscoveryRunning(true);

        Assert.False(follow.Enabled);
    }
}
