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
// Singleton because the surfaces answering "which things match this" and "what do they touch" are
// two renderings of one scope, and an action over a selection has to mean the same on both.
builder.Services.AddSingleton<ScopeService>();
// Singleton because Discovery and the Explorer are two views of one run, and following them on
// different beats would read as two different pictures.
builder.Services.AddSingleton<FollowService>();
// Singleton because a run is chosen on the CodeQL page and aimed from the Inventory.
builder.Services.AddSingleton<RunDraft>();
builder.Services.AddSingleton<RequestDraft>();
builder.Services.AddMudServices();

await builder.Build().RunAsync();
