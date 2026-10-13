namespace Abom.WebAssembly.Services;

public sealed class TutorialRequestHandler(TutorialService tutorial) : DelegatingHandler
{
    protected override async Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request, CancellationToken cancellationToken)
    {
        var control = request.Method != HttpMethod.Get && request.Method != HttpMethod.Head;
        if (control && tutorial.IsReadOnly)
        {
            const string reason = "Exit the tutorial before changing data or starting work. This walkthrough is read-only.";
            tutorial.Report(reason);
            throw new HttpRequestException(reason);
        }
        using var busy = control ? tutorial.BlockStart("Wait for the current operation to finish before starting a tutorial.") : null;
        return await base.SendAsync(request, cancellationToken);
    }
}
