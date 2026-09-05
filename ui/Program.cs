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
// Singleton because Discovery and the Explorer are two views of one run, and following them on
// different beats would read as two different pictures.
builder.Services.AddSingleton<FollowService>();
builder.Services.AddMudServices();

await builder.Build().RunAsync();
