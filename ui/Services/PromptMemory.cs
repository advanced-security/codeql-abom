namespace Abom.WebAssembly.Services;

/// <summary>
/// Which startup check's prompt was answered "Not now", for the life of the tab. A page that asks
/// on every visit turns a decision into a nag; a new check is a new question and asks again.
/// </summary>
public sealed class PromptMemory
{
    private readonly HashSet<string> _dismissed = [];

    public bool Dismissed(string check) => _dismissed.Contains(check);

    public void Dismiss(string check) => _dismissed.Add(check);
}
