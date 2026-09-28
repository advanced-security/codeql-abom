using Abom.WebAssembly;
using Abom.WebAssembly.Services;
using Microsoft.AspNetCore.Components.Web;
using Microsoft.AspNetCore.Components.WebAssembly.Hosting;
using MudBlazor.Services;

var builder = WebAssemblyHostBuilder.CreateDefault(args);
builder.RootComponents.Add<App>("#app");
builder.RootComponents.Add<HeadOutlet>("head::after");

builder.Services.AddScoped(_ => new HttpClient
{
    BaseAddress = new Uri(builder.HostEnvironment.BaseAddress),
});
builder.Services.AddScoped<AbomApi>();
builder.Services.AddScoped<ThemeService>();
builder.Services.AddScoped<AccountService>();
// Shared so every surface reads and acts on the same scope.
builder.Services.AddSingleton<ScopeService>();
// Shared so open surfaces refresh on one follow cadence.
builder.Services.AddSingleton<FollowService>();
builder.Services.AddSingleton<RunDraft>();
builder.Services.AddSingleton<RequestDraft>();
builder.Services.AddMudServices();

await builder.Build().RunAsync();
