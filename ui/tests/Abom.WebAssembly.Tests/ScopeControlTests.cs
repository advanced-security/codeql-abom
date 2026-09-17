using Abom.WebAssembly.Services;
using Abom.WebAssembly.Shared;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// `properties=key:value` is the term every filtering endpoint understands, and until now nothing
/// an operator touches could build one. These pin what the control may offer and what it may claim.
/// </summary>
public class ScopeControlTests : AbomTestContext
{
    // Two keys, a breakdown where most of the organization carries no value, and a bucket that is
    // the organization's default rather than anybody's choice.
    private const string BusinessId = """
        {"configured":"business_id","keys":["business_id","phi-scan"],
         "breakdown":{"name":"business_id","repositories":25,"withValue":8,"withoutValue":17,
          "values":[{"value":"0","repositories":8,"analyzed":3,"isDefault":true},
                    {"value":"3","repositories":5,"analyzed":5}],
          "hasDefault":true,"defaultValue":"0"}}
        """;

    private const string PhiScan = """
        {"configured":"business_id","keys":["business_id","phi-scan"],
         "breakdown":{"name":"phi-scan","repositories":25,"withValue":25,"withoutValue":0,
          "values":[{"value":"scan-pending","repositories":25,"analyzed":9}],
          "hasDefault":false,"defaultValue":""}}
        """;

    private ScopeService Scope => Services.GetService<ScopeService>()!;

    private IRenderedComponent<ScopeControl> RenderControl()
    {
        var page = Render<ScopeControl>();
        page.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/properties")));
        return page;
    }

    /// <summary>
    /// ABOM is told a key and knows nothing about what it means. Offering anything but the keys the
    /// organization administers would be inventing a unit nobody chose.
    /// </summary>
    [Fact]
    public void TheKeysOfferedAreTheOnesTheOrganizationAdministers()
    {
        Answer("api/properties", BusinessId);

        var page = RenderControl();

        Assert.NotNull(page.Find("[data-abom-property-key='business_id']"));
        Assert.NotNull(page.Find("[data-abom-property-key='phi-scan']"));
    }

    /// <summary>
    /// The server answers with the configured key's breakdown, which is a convenience and not an
    /// answer. Writing a term nobody asked for would silently scope every reader to one unit.
    /// </summary>
    [Fact]
    public void ArrivingNarrowsNothing()
    {
        Answer("api/properties", BusinessId);

        RenderControl();

        Assert.Empty(Scope.Spec.Properties);
        Assert.Empty(Scope.Spec.NotProperties);
    }

    [Fact]
    public void ChoosingAValueNarrowsTheScope()
    {
        Answer("api/properties", BusinessId);

        var page = RenderControl();
        page.Find("[data-abom-property-include='business_id:3']").Click();

        Assert.Equal([new PropertyMatch("business_id", "3")], Scope.Spec.Properties);
    }

    /// <summary>
    /// Excluding is a different term, not a negated one: a node an included property still reaches
    /// stays as a dependency, so the two cannot share a control that only flips a sign.
    /// </summary>
    [Fact]
    public void ExcludingAValueWritesTheOtherTerm()
    {
        Answer("api/properties", BusinessId);

        var page = RenderControl();
        page.Find("[data-abom-property-exclude='business_id:0']").Click();

        Assert.Equal([new PropertyMatch("business_id", "0")], Scope.Spec.NotProperties);
        Assert.Empty(Scope.Spec.Properties);
    }

    [Fact]
    public void ASecondKeyNarrowsFurtherRatherThanReplacing()
    {
        Answer("api/properties?property=phi-scan", PhiScan);
        Answer("api/properties", BusinessId);

        var page = RenderControl();
        page.Find("[data-abom-property-include='business_id:3']").Click();
        page.Find("[data-abom-property-key='phi-scan']").Click();
        page.WaitForAssertion(() => page.Find("[data-abom-property-include='phi-scan:scan-pending']"));
        page.Find("[data-abom-property-include='phi-scan:scan-pending']").Click();

        Assert.Equal(
            [new PropertyMatch("business_id", "3"), new PropertyMatch("phi-scan", "scan-pending")],
            Scope.Spec.Properties);
    }

    /// <summary>
    /// The breakdown is read for the key the operator picked. Reading only the configured one would
    /// leave every other key with values nobody can see.
    /// </summary>
    [Fact]
    public void ABreakdownIsReadForTheKeyChosen()
    {
        Answer("api/properties?property=phi-scan", PhiScan);
        Answer("api/properties", BusinessId);

        var page = RenderControl();
        page.Find("[data-abom-property-key='phi-scan']").Click();

        page.WaitForAssertion(() =>
            Assert.Contains(Sent, r => r.Url.Contains("api/properties?property=phi-scan")));
    }

    [Fact]
    public void TheSameValueChosenTwiceIsStillOneTerm()
    {
        Answer("api/properties", BusinessId);

        var page = RenderControl();
        page.Find("[data-abom-property-include='business_id:3']").Click();
        page.Find("[data-abom-property-include='business_id:3']").Click();

        Assert.Equal([new PropertyMatch("business_id", "3")], Scope.Spec.Properties);
    }

    [Fact]
    public void DroppingOneMatchLeavesTheOthers()
    {
        Answer("api/properties", BusinessId);

        var page = RenderControl();
        page.Find("[data-abom-property-include='business_id:0']").Click();
        page.Find("[data-abom-property-include='business_id:3']").Click();
        page.Find("[data-abom-property-drop='business_id:0']").Click();

        Assert.Equal([new PropertyMatch("business_id", "3")], Scope.Spec.Properties);
    }

    /// <summary>
    /// 17 of 25 repositories carry no value for this key, so narrowing by it answers about eight.
    /// That belongs beside the control rather than behind a tooltip nobody opens.
    /// </summary>
    [Fact]
    public void AScopeThatLeavesMostOfTheOrganizationOutSaysSo()
    {
        Answer("api/properties", BusinessId);

        var page = RenderControl();

        Assert.Contains("17 of 25", page.Markup);
    }

    /// <summary>
    /// A required property with a default has no unset state, so its largest bucket is often the one
    /// nobody picked. Counting it like a choice tells a reader eight teams decided when none did.
    /// </summary>
    [Fact]
    public void TheDefaultBucketIsMarkedAsOneNobodyChose()
    {
        Answer("api/properties", BusinessId);

        var page = RenderControl();

        Assert.Contains("organization default", page.Markup);
    }

    /// <summary>
    /// A scope arriving in the address has to render as chosen, or the control and the graph show
    /// different questions on one screen.
    /// </summary>
    [Fact]
    public void AScopeArrivedWithIsShownAsChosen()
    {
        Answer("api/properties", BusinessId);
        Scope.Set(new ViewSpec
        {
            Properties = [new PropertyMatch("business_id", "3")],
            NotProperties = [new PropertyMatch("phi-scan", "scan-pending")],
        });

        var page = RenderControl();

        Assert.NotNull(page.Find("[data-abom-property-drop='business_id:3']"));
        Assert.NotNull(page.Find("[data-abom-property-drop='not phi-scan:scan-pending']"));
    }

    /// <summary>
    /// A key nobody administers has no values to offer, and saying so is the only way an operator
    /// finds out they configured a key the organization does not set.
    /// </summary>
    [Fact]
    public void AKeyTheOrganizationSetsNowhereSaysSoRatherThanShowingNothing()
    {
        Answer("api/properties", """
            {"configured":"business_id","keys":["business_id"],
             "breakdown":{"name":"business_id","repositories":25,"withValue":0,"withoutValue":25,
              "values":[],"hasDefault":false,"defaultValue":""}}
            """);

        var page = RenderControl();

        Assert.Contains("No repository you own carries a value for business_id", page.Markup);
    }

    /// <summary>
    /// An organization that administers no property at all is a different state from one whose key
    /// nobody sets, and offering an empty picker to both says neither.
    /// </summary>
    [Fact]
    public void AnOrganizationWithNoPropertiesAtAllSaysThatInstead()
    {
        Answer("api/properties", """
            {"configured":"","keys":[],
             "breakdown":{"name":"","repositories":25,"withValue":0,"withoutValue":25,
              "values":[],"hasDefault":false,"defaultValue":""}}
            """);

        var page = RenderControl();

        Assert.Contains("sets no custom property", page.Markup);
    }
}
