using Abom.WebAssembly.Services;
using Abom.WebAssembly.Shared;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

public class FollowControlTests : AbomTestContext
{
    // A checkbox here was disabled whenever nothing was running, so no view could be asked to
    // watch for something starting, and a page left on "Pausing…" stayed there until someone
    // reloaded it. Having the page open is the choice to watch it; the only setting is how often.
    [Fact]
    public void TheOpenPageIsTheChoiceToWatch()
    {
        var cut = Render<FollowControl>();

        Assert.Empty(cut.FindAll("input[type=checkbox]"));
    }

    // The interval is a preference, not a property of the current run: setting it before
    // starting anything is the normal case, so it stays settable when nothing is running.
    [Fact]
    public void TheIntervalStaysSettableWhileNothingIsRunning()
    {
        var cut = Render<FollowControl>();

        Assert.DoesNotContain("disabled", cut.Find(".mud-select").OuterHtml);
    }

    [Fact]
    public async Task TheIntervalIsRestoredOnReload()
    {
        JSInterop.Setup<string?>("abomTheme.load", "abom.follow").SetResult("60");
        var follow = Services.GetRequiredService<FollowService>();

        await follow.InitializeAsync();

        Assert.Equal(60, follow.Seconds);
    }

    // Views arm their timers as they render; the layout restores the saved interval afterwards.
    // Unannounced, the restored value is held by nobody and every open view keeps the default
    // beat until the control is touched.
    [Fact]
    public async Task ARestoredIntervalRetimesTheViewsAlreadyOpen()
    {
        JSInterop.Setup<string?>("abomTheme.load", "abom.follow").SetResult("60");
        var follow = Services.GetRequiredService<FollowService>();
        var announced = 0;
        follow.Changed += () => announced++;

        await follow.InitializeAsync();

        Assert.Equal(1, announced);
    }

    [Fact]
    public async Task ChoosingAnIntervalIsRemembered()
    {
        var follow = Services.GetRequiredService<FollowService>();

        await follow.SetSecondsAsync(300);

        var saved = JSInterop.Invocations["abomTheme.save"].Last();
        Assert.Equal(["abom.follow", "300"], saved.Arguments);
    }
}
