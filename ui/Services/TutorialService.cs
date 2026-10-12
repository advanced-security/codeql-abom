using System.Text.Json;
using Microsoft.AspNetCore.Components;
using Microsoft.AspNetCore.Components.Routing;
using Microsoft.JSInterop;

namespace Abom.WebAssembly.Services;

public sealed class TutorialService(IJSRuntime js, NavigationManager nav, ScopeService scope,
    RequestDraft requests, RunDraft runs) : IDisposable
{
    private sealed record Preferences
    {
        public int Version { get; init; } = 1;
        public bool Acknowledged { get; init; }
        public bool Enabled { get; init; }
        public Dictionary<string, int> Completed { get; init; } = [];
    }

    private Preferences _preferences = new();
    private readonly Dictionary<object, string> _blocks = [];
    private readonly Dictionary<string, int> _resumeSteps = [];
    private readonly HashSet<string> _work = [];
    private readonly CancellationTokenSource _lifetime = new();
    private string? _returnAddress;
    private string? _guidedAddress;
    private ViewSpec? _returnScope;
    private IReadOnlyList<string> _returnSelection = [];
    private bool _initializing;

    private string _dataset = "";

    // A Demo's progress belongs to its dataset, so a new Demo, or the operator's own instance at
    // the same address, starts with nothing marked done.
    public string StorageKey => "abom.tutorial.v1:" + nav.BaseUri + (_dataset.Length > 0 ? "#demo:" + _dataset : "");
    public bool Initialized { get; private set; }
    public bool Acknowledged => _preferences.Acknowledged;
    public bool Enabled { get; private set; }
    public bool ShowWelcome => Initialized && Enabled && Active is null;
    public bool IsReadOnly => Active is not null;
    public TutorialLesson? Active { get; private set; }
    public int StepIndex { get; private set; }
    public TutorialStep? Step => Active?.Steps[StepIndex];
    public bool LastStep => Active is not null && StepIndex == Active.Steps.Count - 1;
    public string Notice { get; private set; } = "";
    public string StartRefusal => _blocks.Values.FirstOrDefault() ??
        (requests.Request is not null || runs.Queries.Count > 0 ? "Finish or discard your draft before starting a tutorial." : "");
    public event Action? Changed;

    public async Task InitializeAsync(string? demoDataset = null)
    {
        if (Initialized || _initializing) return;
        _initializing = true;
        _dataset = demoDataset ?? "";
        nav.LocationChanged += LocationChanged;
        runs.Changed += DraftChanged;
        try
        {
            var raw = await js.InvokeAsync<string?>("abomTutorial.load", StorageKey);
            if (raw is not null)
            {
                var saved = JsonSerializer.Deserialize<Preferences>(raw);
                if (saved is not { Version: 1, Completed: not null })
                    throw new JsonException("Unsupported tutorial preferences.");
                _preferences = saved;
            }
        }
        catch (Exception ex) when (ex is JSException or JsonException)
        {
            Notice = $"Tutorial progress could not be read; this tab starts fresh. {ex.Message}";
        }
        Enabled = !Acknowledged;
        Initialized = true;
        Changed?.Invoke();
    }

    public bool Completed(TutorialLesson lesson) =>
        _preferences.Completed.TryGetValue(lesson.Id, out var version) && version == lesson.Version;

    public async Task SetEnabledAsync(bool enabled)
    {
        if (!enabled)
        {
            await ExitAsync();
            return;
        }
        Enabled = true;
        Changed?.Invoke();
        await SaveAsync();
    }

    public bool CanResume(string id) => _resumeSteps.ContainsKey(id);

    public async Task StartAsync(string id, bool restart = false)
    {
        var lesson = TutorialLessons.All.Single(l => l.Id == id);
        if (StartRefusal.Length > 0)
        {
            Report(StartRefusal);
            return;
        }
        _returnAddress = nav.Uri;
        _returnScope = scope.Spec;
        _returnSelection = scope.Selection.ToArray();
        Active = lesson;
        StepIndex = restart ? 0 : _resumeSteps.GetValueOrDefault(id);
        if (restart) _resumeSteps.Remove(id);
        Enabled = true;
        _preferences = _preferences with { Acknowledged = true };
        Changed?.Invoke();
        await SaveAsync();
    }

    public void Next()
    {
        if (Active is null || LastStep) return;
        StepIndex++;
        Changed?.Invoke();
    }

    public void Back()
    {
        if (Active is null || StepIndex == 0) return;
        StepIndex--;
        Changed?.Invoke();
    }

    public void Navigate(string route)
    {
        if (Active is null) return;
        var destination = nav.ToAbsoluteUri(route);
        if (!destination.AbsoluteUri.StartsWith(nav.BaseUri, StringComparison.Ordinal))
            throw new ArgumentException("A tutorial can only navigate within ABOM.", nameof(route));
        _guidedAddress = destination.AbsoluteUri;
        if (nav.Uri != _guidedAddress)
            nav.NavigateTo(route, new NavigationOptions { ReplaceHistoryEntry = true });
    }

    public async Task FinishAsync()
    {
        if (Active is null || !LastStep)
            throw new InvalidOperationException("Finish the walkthrough before marking it complete.");
        _preferences.Completed[Active.Id] = Active.Version;
        _resumeSteps.Remove(Active.Id);
        await EndAsync(restore: true, remember: false);
    }

    public async Task FinishAndStartAsync(string id)
    {
        if (Active is null || !LastStep || !Active.Related.Contains(id))
            throw new InvalidOperationException("Finish the current tutorial before choosing a related one.");
        var next = TutorialLessons.All.Single(l => l.Id == id);
        _preferences.Completed[Active.Id] = Active.Version;
        _resumeSteps.Remove(Active.Id);
        _resumeSteps.Remove(id);
        Active = next;
        StepIndex = 0;
        Changed?.Invoke();
        await SaveAsync();
    }

    public Task ExitAsync(bool restore = true) => EndAsync(restore, remember: true);

    private async Task EndAsync(bool restore, bool remember)
    {
        var address = _returnAddress;
        if (remember && Active is not null) _resumeSteps[Active.Id] = StepIndex;
        Active = null;
        StepIndex = 0;
        Enabled = false;
        _preferences = _preferences with { Acknowledged = true };
        _returnAddress = null;
        _guidedAddress = null;
        if (restore && address is not null)
        {
            if (_returnScope is not null) scope.Set(_returnScope);
            scope.Select(_returnSelection);
            nav.NavigateTo(address, new NavigationOptions { ReplaceHistoryEntry = true });
        }
        Changed?.Invoke();
        await SaveAsync();
    }

    public IDisposable BlockStart(string reason)
    {
        var key = new object();
        _blocks[key] = reason;
        Changed?.Invoke();
        return new Block(() =>
        {
            _blocks.Remove(key);
            if (Notice == reason) Notice = "";
            Changed?.Invoke();
        });
    }

    public void WatchWork(string key, string reason, Func<CancellationToken, Task<bool?>> running)
    {
        if (!_work.Add(key)) return;
        var block = BlockStart(reason);
        const string unknown = "The operation's status could not be read. Tutorial remains unavailable until its status is known.";
        _ = Watch();
        async Task Watch()
        {
            try
            {
                while (true)
                {
                    await Task.Delay(TimeSpan.FromSeconds(3), _lifetime.Token);
                    var state = await running(_lifetime.Token);
                    if (state == false) break;
                    if (state is null)
                        Report(unknown);
                    else if (Notice == unknown)
                        Report(reason);
                }
            }
            catch (OperationCanceledException) when (_lifetime.IsCancellationRequested) { }
            finally
            {
                _work.Remove(key);
                if (Notice == unknown) Notice = "";
                block.Dispose();
            }
        }
    }

    private sealed class Block(Action release) : IDisposable
    {
        private bool _disposed;
        public void Dispose()
        {
            if (_disposed) return;
            _disposed = true;
            release();
        }
    }

    public void Report(string message)
    {
        Notice = message;
        Changed?.Invoke();
    }

    private void DraftChanged() => Changed?.Invoke();

    private async Task SaveAsync()
    {
        _preferences = _preferences with { Enabled = Enabled };
        try
        {
            await js.InvokeVoidAsync("abomTutorial.save", StorageKey, JsonSerializer.Serialize(_preferences));
        }
        catch (JSException ex)
        {
            Report($"Tutorial progress cannot be remembered outside this tab. {ex.Message}");
        }
    }

    private async void LocationChanged(object? sender, LocationChangedEventArgs args)
    {
        if (Active is not null && args.Location != _guidedAddress)
            await ExitAsync(restore: false);
    }

    public void Dispose()
    {
        nav.LocationChanged -= LocationChanged;
        runs.Changed -= DraftChanged;
        _lifetime.Cancel();
        _lifetime.Dispose();
    }
}
