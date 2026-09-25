using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// One scope, reprojected. A repository, a dependency and a relationship are different shapes
/// rather than different scopes, so they are units of one surface rather than pages of their own —
/// and a unit's columns have to be its own, or it draws absence as zero.
/// </summary>
public class InventoryUnitTests : AbomTestContext
{
    private const string Views = """
        [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]
        """;

    private const string Dependencies = """
        {"items":[{"nodeKey":"actions/checkout","kind":"action","origin":"external",
          "dependentRepos":50,"directDependents":60,"distinctRefs":3,
          "findings":2,"advisories":0,"analyzed":true,"refs":["v4"]}],"total":1585}
        """;

    private const string Repositories = """
        {"items":[{"fullName":"octo-org/billing","origin":"seed","workflows":4,
          "dependencies":37,"findings":9,"exposures":3,"directExposures":1}],"total":193}
        """;

    private const string Properties = """
        {"configured":"business_id","keys":["business_id"],
         "breakdown":{"name":"business_id","repositories":12,"withValue":12,"withoutValue":0,
           "values":[{"value":"0","repositories":8,"isDefault":true},
                     {"value":"3","repositories":4,"isDefault":false}],
           "hasDefault":true,"defaultValue":"0"}}
        """;

    private const string Advisories = """
        {"items":[{"ghsaId":"GHSA-aaaa-bbbb-cccc","cveId":"CVE-2024-1","severity":"high",
          "summary":"A thing","directRepos":2,"transitiveRepos":5,"actions":["actions/checkout"],
          "cvssV3Score":7.5,"hasCvss":true,"epssPercentage":0.1,"hasEpss":true,
          "fixable":true,"rank":4}],"total":3,"unreached":8}
        """;

    private const string Relationships = """
        {"items":[{"edgeKey":"octo-org/app:.github/workflows/ci.yml:20:15",
          "kind":"step-uses-action","source":"step:octo-org/app:ci.yml::1",
          "target":"actions/setup-node","ref":"v4","refStyle":"tag",
          "boundary":"cross-owner","repository":"octo-org/app",
          "filePath":".github/workflows/ci.yml","startLine":20,
          "findings":1,"rules":["actions/unpinned-tag"],
          "advisoryRoute":false,"findingRoute":true,"ownedDepth":1}],"total":37}
        """;

    public InventoryUnitTests()
    {
        Answer("api/views", Views);
        Answer("api/repositories", Repositories);
        Answer("api/actions", Dependencies);
        Answer("api/remediation", Advisories);
        Answer("api/relationships", Relationships);
    }

    private IRenderedComponent<Pages.Inventory> RenderInventory()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/actions")));
        return page;
    }

    [Fact]
    public void TheUnitsOnOfferAreTheOnesWithColumnsOfTheirOwn()
    {
        var page = RenderInventory();

        Assert.NotNull(page.Find("[data-abom-unit='dependencies']"));
        Assert.NotNull(page.Find("[data-abom-unit='repositories']"));
    }

    /// <summary>
    /// Each unit asks its own endpoint. Reprojecting in the browser would mean showing a repository
    /// the columns of a dependency, which is the absent-as-zero screen this replaced.
    /// </summary>
    [Fact]
    public void ChoosingAUnitAsksTheEndpointThatAnswersAboutIt()
    {
        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() =>
            Assert.Contains(Sent, r => r.Url.Contains("api/repositories?")));
    }

    /// <summary>
    /// A repository is not a node, so it gets repository columns. Showing "Repos reached" against a
    /// repository would be a measurement nothing made.
    /// </summary>
    [Fact]
    public void TheRepositoriesUnitRendersRepositoryColumns()
    {
        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() =>
        {
            Assert.Contains("octo-org/billing", page.Markup);
            Assert.Contains("Workflows", page.Markup);
            Assert.DoesNotContain("Repos reached", page.Markup);
        });
    }

    /// <summary>
    /// A relationship is a line in a file, not a thing in the corpus. Its columns are the
    /// reference as written and what was reported against the line itself — neither of which any
    /// unit of nodes carries, which is why it had to become a unit rather than a filter.
    /// </summary>
    [Fact]
    public void TheRelationshipsUnitRendersTheLineAndWhatFiredOnIt()
    {
        var page = RenderInventory();
        page.Find("[data-abom-unit='relationships']").Click();

        page.WaitForAssertion(() =>
        {
            Assert.Contains(Sent, r => r.Url.Contains("api/relationships?"));
            Assert.Contains("actions/setup-node", page.Markup);
            Assert.Contains(".github/workflows/ci.yml:20", page.Markup);
            Assert.Contains("actions/unpinned-tag", page.Markup);
            // Ordering is not use, so the list must not present either as the other.
            Assert.Contains("step uses", page.Markup);
        });
    }

    /// <summary>
    /// The scope survives the switch, or a unit is a page wearing a different hat.
    /// </summary>
    [Fact]
    public void TheScopeSurvivesChangingUnit()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?properties=business_id%3A3");

        var page = RenderInventory();
        page.Find("[data-abom-unit='repositories']").Click();

        page.WaitForAssertion(() => Assert.Contains(
            "properties=business_id%3A3",
            Sent.Last(r => r.Url.Contains("api/repositories?")).Url));
    }

    /// <summary>
    /// The unit travels in the address, so a link hands over the same screen the sender was on.
    /// </summary>
    [Fact]
    public void TheUnitArrivedWithIsTheUnitShown()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?unit=repositories");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
            Assert.Contains(Sent, r => r.Url.Contains("api/repositories?")));
    }

    /// <summary>
    /// Every unit gets its own heading. A third unit added while the heading asked about two
    /// landed in the else branch, so a list of advisories sat under "Actions &amp; Workflows" and
    /// a paragraph about external dependencies. Every test was green; the browser read it in one
    /// glance, which is why this asserts the name of each unit rather than the shape of two.
    /// </summary>
    [Theory]
    [InlineData("dependencies", "Actions")]
    [InlineData("workflows", "Workflows")]
    [InlineData("jobs", "Jobs")]
    [InlineData("repositories", "Repositories")]
    [InlineData("advisories", "Advisories")]
    public void TheHeadingNamesTheUnitOnScreen(string unit, string heading)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
            Assert.Equal(heading, page.Find("h1,h2,h3,h4,h5,h6").TextContent.Trim()));
    }

    /// <summary>
    /// An entity written into a C# string is text, not markup: the heading rendered the five
    /// characters "&amp;amp;" on screen. Escaping is the renderer's job, and a literal entity
    /// anywhere in the page is the same bug wherever it appears.
    /// </summary>
    [Theory]
    [InlineData("dependencies")]
    [InlineData("repositories")]
    [InlineData("advisories")]
    public void NoEntityIsRenderedAsItsOwnText(string unit)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.DoesNotContain("&amp;amp;", page.Markup));
    }

    /// <summary>
    /// An advisory's rank is its place in the whole match, which the server knows and a page does
    /// not: the first row of a later page is not first.
    /// </summary>
    [Fact]
    public void TheAdvisoriesUnitShowsEachRowsRankInTheWholeMatch()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?unit=advisories");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Equal("4",
            page.Find("[data-abom-advisory-rank]").TextContent.Trim()));
    }

    /// <summary>
    /// The line under the grid claims an order. Naming the dependency default under a unit ordered
    /// by something else tells the reader the ranking is one thing while it is another — and the
    /// ranking is the only reason to read the top of a list at all.
    /// </summary>
    [Theory]
    [InlineData("dependencies", "reach")]
    [InlineData("repositories", "findings")]
    [InlineData("advisories", "severity, then reach")]
    public void TheOrderStatedIsTheOrderInForce(string unit, string order)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(
            $"ordered by {order} across everything that matched",
            System.Text.RegularExpressions.Regex.Replace(page.Markup, @"\s+", " ")));
    }

    /// <summary>
    /// The chosen unit has to be the most prominent of the three. Filled-on-Default renders as a
    /// dark fill with no border in the dark scheme, so the selected control read as the faintest
    /// thing on the row — technically distinct, and backwards to anyone looking at it.
    /// </summary>
    [Theory]
    [InlineData("dependencies")]
    [InlineData("repositories")]
    [InlineData("advisories")]
    public void TheChosenUnitIsTheProminentOne(string unit)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo($"/inventory?unit={unit}");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var chosen = page.Find($"[data-abom-unit='{unit}']");
            Assert.Contains("mud-button-filled-primary", chosen.ClassName);

            foreach (var other in page.FindAll("[data-abom-unit]")
                         .Where(b => b.GetAttribute("data-abom-unit") != unit))
            {
                Assert.DoesNotContain("mud-button-filled", other.ClassName);
            }
        });
    }

    /// <summary>
    /// The view in force is marked in the scope bar, for the same reason and in the same scheme.
    /// </summary>
    [Fact]
    public void TheSelectedViewIsMarked()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?scope=owned");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains(
            "abom-view-selected", page.Find("[data-abom-view='owned']").ClassName));
    }

    /// <summary>
    /// The surface that owns the scope has to carry the control that builds it. Inventory applied
    /// a property scope out of the address and said nothing about it: the counts narrowed, the
    /// reader was given no way to see what narrowed them, and no way to narrow further without
    /// editing the URL. The Explorer had the control all along, which is how it went unnoticed.
    /// </summary>
    [Fact]
    public void TheSurfaceThatOwnsTheScopeCarriesTheControlThatBuildsIt()
    {
        Answer("api/properties", Properties);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
                .NavigateTo("/inventory?properties=business_id%3A3");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() =>
        {
            var chip = page.Find("[data-abom-filter-chip='properties']");
            Assert.Contains("business_id:3", chip.TextContent);
            Assert.NotNull(chip.QuerySelector("[data-abom-chip-remove]"));
        });
    }

    /// <summary>
    /// The heading describes what is listed. It read "Every external dependency the organization
    /// reaches" over a grid where, on the real corpus, 1,332 of 1,585 rows were repository-hosted
    /// workflows and composite actions, and 139 of those belonged to the organization itself. A
    /// reader who trusts the sentence mistakes their own workflows for somebody else's code.
    /// </summary>
    [Fact]
    public void TheDependenciesHeadingDoesNotCallOwnedRowsExternal()
    {
        Answer("api/actions", """
            {"items":[{"nodeKey":"octo-org/app/.github/workflows/ci.yml","kind":"workflow",
              "origin":"seed","dependentRepos":0,"directDependents":0,"distinctRefs":0,
              "findings":3,"advisories":0,"analyzed":true,"refs":[]}],"total":1}
            """);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo("/inventory");

        var page = Render<Pages.Inventory>();

        page.WaitForAssertion(() => Assert.Contains("octo-org/app", page.Markup));
        Assert.DoesNotContain("Every external dependency", page.Markup);
    }
}
