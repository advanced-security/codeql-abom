using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>Reads once what the server is, so every page labels a Demo the same way.</summary>
public sealed class InstanceService(AbomApi api)
{
    private Task<InstanceInfo>? _load;

    public InstanceInfo Info { get; private set; } = new();
    public bool IsDemo => Info.IsDemo;
    public event Action? Changed;

    public Task<InstanceInfo> LoadAsync() => _load ??= Read();

    private async Task<InstanceInfo> Read()
    {
        // A server that predates the endpoint is an ordinary instance.
        Info = await api.GetInstanceAsync() ?? new InstanceInfo();
        Changed?.Invoke();
        return Info;
    }
}
