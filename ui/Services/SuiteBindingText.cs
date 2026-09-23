using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>Shared by the grid and Settings, so one binding is never worded two ways.</summary>
public static class SuiteBindingText
{
    /// <summary>Owned only with an identity: without one, zero would claim nothing is owned.</summary>
    public static string[] Tiers(SuiteBinding? binding) => binding?.Verified == true
        ? ["owned", "direct", "transitive"]
        : ["direct", "transitive"];

    public static IReadOnlyList<string> BoundTo(SuiteBinding? binding, string tier) =>
        binding is not null && binding.Tiers.TryGetValue(tier, out var s) ? s : [];

    /// <summary>The suite's own file name; the stored path is what an operator would grep for.</summary>
    public static string Label(string suite)
    {
        var name = suite.Split('/').LastOrDefault() ?? suite;
        return name.EndsWith(".qls", StringComparison.Ordinal) ? name[..^4] : name;
    }

    public static string Summary(SuiteBinding binding) => string.Join(" · ", Tiers(binding).Select(tier =>
    {
        var bound = BoundTo(binding, tier);
        return bound.Count == 0
            ? $"{tier}: nothing"
            : $"{tier}: {string.Join(", ", bound.Select(Label))}";
    }));
}
