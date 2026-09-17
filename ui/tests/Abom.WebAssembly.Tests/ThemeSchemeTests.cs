using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The page background is painted from a <c>data-theme</c> attribute index.html sets before
/// Blazor exists, and MudBlazor knows nothing about it. Whenever the two disagree the interface
/// inverts: dark components on a white page, or the reverse.
/// </summary>
public class ThemeSchemeTests : AbomTestContext
{
    private static bool Resolved(JSRuntimeInvocation invocation) => (bool)invocation.Arguments[0]!;

    [Fact]
    public async Task ChoosingASchemeRepaintsThePage()
    {
        var theme = Services.GetRequiredService<ThemeService>();

        await theme.SetAsync(ThemeChoice.Dark, systemPrefersDark: false);

        Assert.True(Resolved(JSInterop.VerifyInvoke("abomTheme.applyResolved")));
    }

    // Following the system means following it as it changes, and the attribute is the only thing
    // painting the background: without this the components flip and the page stays behind.
    [Fact]
    public void TheSystemChangingItsMindRepaintsThePage()
    {
        var theme = Services.GetRequiredService<ThemeService>();

        theme.SystemPreferenceChanged(true);

        Assert.True(Resolved(JSInterop.VerifyInvoke("abomTheme.applyResolved")));
    }
}
