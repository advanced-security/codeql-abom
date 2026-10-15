using Abom.WebAssembly.Pages;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>The lifecycle, its audit evidence, and the crawl's suites.</summary>
public class CodeQLTabsTests : AbomTestContext
{
    public CodeQLTabsTests()
    {
        Answer("api/accounts", """{"authenticated":true,"login":"operator","accounts":[]}""");
        Fallback("api/query-requests", """{"requests":[],"kinds":["","missed"],"statuses":["open"]}""");
        Fallback("api/queries", """{"queries":[],"stages":["dev"]}""");
        Fallback("api/runs", """{"runs":[]}""");
    }

    /// <summary>Asked for, then written, then run: the tabs follow that order.</summary>
    [Fact]
    public void ThePartsAreRequestsCatalogRunsAuditAndSuites()
    {
        var page = Render<CodeQL>();

        var tabs = page.FindAll("[data-abom-tab]").Select(t => t.GetAttribute("data-abom-tab")).ToList();
        Assert.Equal(["requests", "catalog", "runs", "audit", "suites"], tabs);
        Assert.Equal("requests", page.Find("[aria-selected='true']").GetAttribute("data-abom-tab"));
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-requests]")));
    }

    /// <summary>The part in view travels in the address, so a link opens the same one.</summary>
    [Fact]
    public void ChoosingAPartPutsItInTheAddress()
    {
        var page = Render<CodeQL>();

        page.Find("[data-abom-tab='runs']").Click();

        var at = Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri;
        Assert.Contains("tab=runs", at);
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-runs]")));
    }

    [Fact]
    public void ThePartInTheAddressIsThePartShown()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/codeql?tab=catalog");

        var page = Render<CodeQL>();

        Assert.Equal("catalog", page.Find("[aria-selected='true']").GetAttribute("data-abom-tab"));
    }

    /// <summary>A request has an address, and following it opens that request's row.</summary>
    [Fact]
    public void AnAddressNamingARequestOpensItsRow()
    {
        Answer("api/query-requests", """
            {"requests":[
              {"rowId":3,"description":"first","compliant":"c","nonCompliant":"n","kind":"missed",
               "status":"open","requestedBy":"operator","steps":[],"evidence":[]},
              {"rowId":8,"description":"second","compliant":"c","nonCompliant":"n","kind":"missed",
               "status":"open","requestedBy":"operator","steps":[],"evidence":[]}],
             "kinds":["","missed"],"statuses":["open"]}
            """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/codeql?tab=requests&request=8");

        var page = Render<CodeQL>();

        page.WaitForAssertion(() =>
        {
            var open = Assert.Single(page.FindAll("[data-abom-request-open]"));
            Assert.Equal("8", open.GetAttribute("data-abom-request-open"));
        });
    }
}
