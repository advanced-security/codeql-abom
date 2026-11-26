using System.Net;
using System.Net.Http.Json;
using System.Text.Json;
using Abom.WebAssembly.Models;
using Microsoft.AspNetCore.Components;

namespace Abom.WebAssembly.Services;

/// <summary>
/// Sends the browser to sign in when the API says it must, remembering where it was. Only a problem
/// whose code says a session is needed does this; any other refusal is the page's to explain.
/// </summary>
public sealed class SignInRequestHandler(NavigationManager navigation) : DelegatingHandler
{
    protected override async Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request, CancellationToken cancellationToken)
    {
        var response = await base.SendAsync(request, cancellationToken);
        if (response.StatusCode != HttpStatusCode.Unauthorized)
        {
            return response;
        }
        await response.Content.LoadIntoBufferAsync(cancellationToken);
        ApiProblem? problem = null;
        try
        {
            problem = await response.Content.ReadFromJsonAsync<ApiProblem>(cancellationToken);
        }
        catch (JsonException)
        {
            // Not the server's problem shape, so not its sign-in refusal either.
        }
        if (problem is not { Code: "sign-in-required" })
        {
            return response;
        }
        var here = "/" + navigation.ToBaseRelativePath(navigation.Uri);
        navigation.NavigateTo("auth/login?return=" + Uri.EscapeDataString(here), forceLoad: true);
        return response;
    }
}
