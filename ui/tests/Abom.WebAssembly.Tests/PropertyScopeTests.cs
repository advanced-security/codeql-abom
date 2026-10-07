using System.Net;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Declaring the repositories of an organization that carry chosen custom property values. Each
/// property scope is its own line, any of which covers a repository; a scope says what it covers,
/// why it counts nothing when held, and when it takes in repositories nobody chose for it.
/// </summary>
public class PropertyScopeTests : AbomTestContext
{
    private const string Identity = """
        {"authenticated":true,"login":"data-douser","host":"github.com",
         "verifiedAt":"2026-10-06T18:00:00Z","problem":null,"canReadMemberships":true,
         "accounts":[{"owner":"data-douser","kind":"user","role":"owner","canDeclare":true},
                     {"owner":"ops-team","kind":"organization","role":"admin","canDeclare":true}]}
        """;

    private const string DeclaresByProperty = """
        [{"owner":"ops-team","kind":"organization","enabled":true,"includesOnly":true,
          "includes":["tooling"],"declaredAt":"2026-10-06T00:00:00Z","declaredBy":"interface",
          "state":"confirmed",
          "properties":[
            {"terms":[{"name":"github-codeql-config-file","value":"remote=ops-team/configs@main:np.yml"}],
             "declaredBy":"interface","declaredAt":"2026-10-06T00:00:00Z","held":false,"matching":2},
            {"terms":[{"name":"team","value":"Platform"},{"name":"tier","value":"1"}],
             "declaredBy":"interface","declaredAt":"2026-10-06T00:00:00Z","held":false,"matching":3,
             "defaults":["tier"]},
            {"terms":[{"name":"business_id","value":"ABOM"}],
             "declaredBy":"interface","declaredAt":"2026-10-06T00:00:00Z","held":true,
             "heldReason":"business_id can be set by repository administrators, so it cannot decide what is yours",
             "matching":0}],
          "enumeration":{"route":"graphql","listedAt":"2026-10-06T10:00:00Z","sweptAt":"",
            "highCreated":"","highPushed":"","missing":[],
            "propertiesJudgedAt":"2026-10-06T10:00:00Z","propertiesUnjudged":3}}]
        """;

    private const string OpsTeamProperties = """
        {"owner":"ops-team","repositories":12,"problem":null,"clauseCounts":[2,4,0],
         "definitions":[
          {"name":"business_id","valueType":"string","description":"","declarable":false,
           "reason":"business_id can be set by repository administrators, so it cannot decide what is yours",
           "values":[]},
          {"name":"github-codeql-config-file","valueType":"string","description":"Which CodeQL configuration applies",
           "declarable":true,"values":[
            {"value":"remote=ops-team/configs@main:default.yml","repositories":9,"isDefault":true},
            {"value":"remote=ops-team/configs@main:np.yml","repositories":2,"isDefault":false}]},
          {"name":"team","valueType":"single_select","description":"","declarable":true,"values":[
            {"value":"Platform","repositories":5,"isDefault":false},
            {"value":"Infra","repositories":4,"isDefault":false},
            {"value":"Unassigned","repositories":3,"isDefault":true}]},
          {"name":"tags","valueType":"multi_select","description":"","declarable":true,"values":[
            {"value":"x","repositories":2,"isDefault":false},
            {"value":"y","repositories":1,"isDefault":false}]}]}
        """;

    private const string NoImpact = """
        {"rootsNow":0,"rootsNext":0,"closureNow":0,"closureNext":0,
         "entering":[],"leaving":[],"enteringCount":0,"leavingCount":0,"truncated":false,
         "queuedNow":0,"queuedNext":0,"notJudged":0}
        """;

    private void Serve(string scope = DeclaresByProperty, string preview = NoImpact,
        string enumeration = """{"added":0,"promoted":0,"unchanged":0,"accounts":[],"failed":[]}""")
    {
        Answer("api/owned-scope/preview", preview);
        Answer("api/owned-scope/enumerate", enumeration);
        Answer("api/owned-scope", scope);
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", Identity);
    }

    private IEnumerable<SentRequest> PropertyReads =>
        Sent.Where(r => r.Method == "POST" && r.Url.Contains("/properties"));

    private IEnumerable<SentRequest> Writes =>
        Sent.Where(r => r.Method == "PUT" && r.Url.Contains("api/owned-scope"));

    private IRenderedComponent<Pages.Settings> Arrive()
    {
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-property-scope]")));
        return cut;
    }

    private static List<AngleSharp.Dom.IElement> Lines(IRenderedComponent<Pages.Settings> cut) =>
        cut.FindAll("[data-abom-property-scopes='ops-team'] [data-abom-property-scope]").ToList();

    private void OpenEditor(IRenderedComponent<Pages.Settings> cut)
    {
        AnswerWrite("api/accounts/ops-team/properties", OpsTeamProperties);
        cut.Find("[data-abom-add-scope='ops-team']").Click();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-property-choice='team']")));
    }

    private static void Pick(IRenderedComponent<Pages.Settings> cut, string property, string value) =>
        cut.Find($"[data-abom-property-choice='{property}'] [data-abom-property-value='{value}'] input").Change(true);

    // Each scope is a line of its own, joined to the named repositories and to each other by "or",
    // with its terms exactly as GitHub spells them: a key is matched against GitHub's own settings.
    [Fact]
    public void EachPropertyScopeIsALineJoinedByOr()
    {
        Serve();
        var cut = Arrive();

        var lines = Lines(cut);
        Assert.Equal(3, lines.Count);
        Assert.All(lines, line => Assert.StartsWith("or carrying", line.TextContent.Trim()));
        Assert.Contains("github-codeql-config-file = remote=ops-team/configs@main:np.yml", lines[0].TextContent);
        Assert.Contains("team = Platform and tier = 1", lines[1].TextContent);
        Assert.Contains("2 repositories carry it", lines[0].QuerySelector("[data-abom-scope-count]")!.TextContent);
    }

    // A scope with no named repositories before it is the first thing the account covers.
    [Fact]
    public void AScopeWithNothingNamedBeforeItIsNotJoinedByOr()
    {
        Serve(DeclaresByProperty.Replace("""["tooling"]""", "[]"));
        var cut = Arrive();

        var lines = Lines(cut);
        Assert.StartsWith("carrying", lines[0].TextContent.Trim());
        Assert.StartsWith("or carrying", lines[1].TextContent.Trim());
        Assert.DoesNotContain("covers nothing", cut.Find("[data-abom-includes='ops-team']").ParentElement!.TextContent);
    }

    // A held scope is kept so it can be corrected, and counts nothing; a count beside it would read
    // as what it covers.
    [Fact]
    public void AHeldScopeSaysWhyAndCountsNothing()
    {
        Serve();
        var cut = Arrive();

        var held = Lines(cut)[2];
        Assert.Contains("repository administrators", held.QuerySelector("[data-abom-scope-held]")!.TextContent);
        Assert.Null(held.QuerySelector("[data-abom-scope-count]"));
        Assert.Null(Lines(cut)[0].QuerySelector("[data-abom-scope-held]"));
    }

    // Naming the value a repository carries for choosing nothing takes in every repository nobody
    // set, and every new one. That is said on the line, not behind a tooltip.
    [Fact]
    public void AScopeNamingTheDefaultSaysItTakesInWhatNobodyChose()
    {
        Serve();
        var cut = Arrive();

        var lines = Lines(cut);
        var warning = lines[1].QuerySelector("[data-abom-scope-default]");
        Assert.NotNull(warning);
        Assert.Contains("tier", warning!.TextContent);
        Assert.Contains("created later", warning.TextContent);
        Assert.Null(lines[0].QuerySelector("[data-abom-scope-default]"));
    }

    // Unknown is not a mismatch: what the last judgement could not decide is counted beside when it
    // ran, so a short scope is not read as a small estate.
    [Fact]
    public void TheAccountSaysWhenItsScopesWereJudgedAndWhatWasNot()
    {
        Serve();
        var cut = Arrive();

        var judged = cut.Find("[data-abom-scopes-judged='ops-team']");
        Assert.Contains("3 repositories not judged", judged.TextContent);
        Assert.NotNull(judged.QuerySelector("time[datetime='2026-10-06T10:00:00Z']"));
    }

    [Fact]
    public void ScopesNeverJudgedSaySoRatherThanCountingNothing()
    {
        Serve(DeclaresByProperty.Replace(
            "\"propertiesJudgedAt\":\"2026-10-06T10:00:00Z\",\"propertiesUnjudged\":3", "\"propertiesUnjudged\":0"));
        var cut = Arrive();

        // An account the App cannot read is never listed, so the line states why rather than
        // promising a listing that may not come.
        var line = cut.Find("[data-abom-scopes-judged='ops-team']").TextContent;
        Assert.Contains("not judged yet", line);
        Assert.Contains("no full listing has read them", line);
    }

    // Reading an organization's properties spends the operator's token, so nothing is read until
    // they ask; the read counts each scope already drafted against what GitHub says now.
    [Fact]
    public void NoPropertyIsReadUntilTheReaderAsksToAddAScope()
    {
        Serve();
        var cut = Arrive();
        Assert.Empty(PropertyReads);

        OpenEditor(cut);

        var read = Assert.Single(PropertyReads);
        Assert.Contains("\"clauses\"", read.Body);
        Assert.Contains("remote=ops-team/configs@main:np.yml", read.Body);
        Assert.Contains("4 carry it on GitHub now", Lines(cut)[1].QuerySelector("[data-abom-scope-count]")!.TextContent);
    }

    // A property repository administrators can set is listed, so the reader learns why it is not
    // offered, and carries no control a reader could mistake for one that works.
    [Fact]
    public void APropertyThatMayNotScopeIsListedWithItsReasonAndNothingToChoose()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);

        var refused = cut.Find("[data-abom-property-choice='business_id']");
        Assert.Contains("repository administrators", refused.QuerySelector("[data-abom-property-refused]")!.TextContent);
        Assert.Empty(refused.QuerySelectorAll("input"));
        Assert.NotEmpty(cut.FindAll("[data-abom-property-choice='team'] input"));
    }

    // Values are offered verbatim with how many repositories carry each now, and the one carried for
    // choosing nothing is marked where it is chosen.
    [Fact]
    public void EachValueIsOfferedWithHowManyCarryIt()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);

        var platform = cut.Find("[data-abom-property-choice='team'] [data-abom-property-value='Platform']");
        Assert.Contains("Platform", platform.TextContent);
        Assert.Contains("5 repositories", platform.TextContent);
        Assert.Null(platform.QuerySelector("[data-abom-value-default]"));
        Assert.NotNull(cut.Find("[data-abom-property-choice='team'] [data-abom-property-value='Unassigned'] [data-abom-value-default]"));
    }

    // The scope being built is what gets added and then saved, alongside the scopes already there.
    [Fact]
    public void AScopeBuiltFromChosenValuesIsWhatGetsSaved()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);

        Pick(cut, "team", "Infra");
        Pick(cut, "tags", "x");
        Assert.Contains("team = Infra and tags = x", cut.Find("[data-abom-scope-draft]").TextContent);
        cut.Find("[data-abom-scope-add]").Click();

        cut.WaitForAssertion(() => Assert.Equal(4, Lines(cut).Count));
        Assert.Contains("team = Infra and tags = x", Lines(cut)[3].TextContent);
        cut.Find("[data-abom-save]").Click();
        cut.WaitForAssertion(() => Assert.Single(Writes));
        var body = Writes.Single().Body;
        Assert.Contains("\"Infra\"", body);
        Assert.Contains("\"Platform\"", body);
        Assert.Contains("remote=ops-team/configs@main:np.yml", body);
    }

    // Adding a scope counts it, with the others, against what GitHub says now.
    [Fact]
    public void AddingAScopeCountsItAgainstGitHub()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);

        Pick(cut, "team", "Infra");
        cut.Find("[data-abom-scope-add]").Click();

        cut.WaitForAssertion(() => Assert.Equal(2, PropertyReads.Count()));
        Assert.Contains("\"Infra\"", PropertyReads.Last().Body);
    }

    // A property holding one value cannot be required to hold two, so choosing another value of it
    // replaces the first; a multi-select may be required to carry several.
    [Fact]
    public void ChoosingAnotherValueOfASingleValuePropertyReplacesTheFirst()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);

        Pick(cut, "team", "Platform");
        Pick(cut, "team", "Infra");
        Pick(cut, "tags", "x");
        Pick(cut, "tags", "y");

        var draft = cut.Find("[data-abom-scope-draft]").TextContent;
        Assert.DoesNotContain("Platform", draft);
        Assert.Contains("team = Infra and tags = x and tags = y", draft);
    }

    // The warning comes before the scope is added, while choosing the default can still be undone.
    [Fact]
    public void ChoosingTheDefaultWarnsBeforeItIsAdded()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);
        Assert.Empty(cut.FindAll("[data-abom-draft-default]"));

        Pick(cut, "github-codeql-config-file", "remote=ops-team/configs@main:default.yml");

        var warning = cut.Find("[data-abom-draft-default]").TextContent;
        Assert.Contains("github-codeql-config-file", warning);
        Assert.Contains("9", warning);
        Assert.Contains("created later", warning);
    }

    // A scope not yet saved has no recorded judgement, so what GitHub says now carries its warning.
    [Fact]
    public void AnAddedScopeNamingTheDefaultKeepsItsWarning()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);

        Pick(cut, "team", "Unassigned");
        cut.Find("[data-abom-scope-add]").Click();

        cut.WaitForAssertion(() => Assert.Equal(4, Lines(cut).Count));
        Assert.Contains("team", Lines(cut)[3].QuerySelector("[data-abom-scope-default]")!.TextContent);
    }

    // A disabled button that does not say why reads as broken; one already declared says so.
    [Fact]
    public void AScopeAlreadyDeclaredSaysSoInsteadOfAddingItAgain()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);
        Assert.Empty(cut.FindAll("[data-abom-scope-duplicate]"));

        Pick(cut, "github-codeql-config-file", "remote=ops-team/configs@main:np.yml");

        Assert.True(cut.Find("[data-abom-scope-add]").HasAttribute("disabled"));
        Assert.Contains("already", cut.Find("[data-abom-scope-duplicate]").TextContent);
    }

    [Fact]
    public void NothingIsAddedUntilAValueIsChosen()
    {
        Serve();
        var cut = Arrive();
        OpenEditor(cut);

        Assert.True(cut.Find("[data-abom-scope-add]").HasAttribute("disabled"));
    }

    [Fact]
    public void RemovingAScopeIsWhatGetsSaved()
    {
        Serve();
        var cut = Arrive();

        Lines(cut)[2].QuerySelector("[data-abom-remove-scope]")!.Click();
        cut.WaitForAssertion(() => Assert.Equal(2, Lines(cut).Count));
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Single(Writes));
        Assert.DoesNotContain("ABOM", Writes.Single().Body);
        Assert.Contains("Platform", Writes.Single().Body);
    }

    // A person's own account has no custom properties, so nothing offers to scope it by one.
    [Fact]
    public void AUserAccountOffersNoPropertyScope()
    {
        Serve("""
            [{"owner":"data-douser","kind":"user","enabled":true,"includesOnly":true,
              "includes":["dotfiles"],"declaredAt":"2026-10-06T00:00:00Z","declaredBy":"interface",
              "state":"confirmed","properties":[]}]
            """);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-includes='data-douser']")));

        Assert.Empty(cut.FindAll("[data-abom-add-scope]"));
    }

    [Fact]
    public void APropertyReadGhRefusedNamesTheRemedy()
    {
        Serve();
        var cut = Arrive();
        AnswerWrite("api/accounts/ops-team/properties", """
            {"owner":"ops-team","repositories":0,"definitions":[],"clauseCounts":[],
             "problem":{"reason":"ops-team requires single sign-on, and the gh token is not authorized for it.",
                        "remedy":"Open https://github.com/orgs/ops-team/sso to start a session."}}
            """);

        cut.Find("[data-abom-add-scope='ops-team']").Click();

        cut.WaitForAssertion(() => Assert.Contains("orgs/ops-team/sso", cut.Find("[data-abom-scope-editor='ops-team']").TextContent));
        Assert.Empty(cut.FindAll("[data-abom-scope-add]"));
    }

    // A candidate scope over repositories whose values were never read can neither take them in nor
    // let them go, and the preview says how many that is rather than reporting nothing entering.
    [Fact]
    public void ThePreviewSaysWhatACandidateScopeCouldNotJudge()
    {
        Serve(preview: NoImpact.Replace("\"notJudged\":0", "\"notJudged\":5"));
        var cut = Arrive();

        Lines(cut)[0].QuerySelector("[data-abom-remove-scope]")!.Click();

        cut.WaitForAssertion(() =>
        {
            var unjudged = cut.Find("[data-abom-preview-unjudged]").TextContent;
            Assert.Contains("5", unjudged);
            Assert.Contains("until a listing of the account reads them", unjudged);
        });
    }

    // A held scope counts nothing in the preview, so the preview names it with the reason; without
    // it, a scope that saving would bring into effect reads as one that drops everything.
    [Fact]
    public void ThePreviewNamesACandidateScopeThatCountsNothing()
    {
        Serve(preview: NoImpact.Replace("\"notJudged\":0", """
            "notJudged":0,"held":[{"owner":"ops-team","terms":[{"name":"team","value":"Platform"},{"name":"tier","value":"1"}],
              "reason":"ops-team's custom properties have not been read yet, so team cannot be judged"}]
            """));
        var cut = Arrive();

        Lines(cut)[2].QuerySelector("[data-abom-remove-scope]")!.Click();

        cut.WaitForAssertion(() =>
        {
            var held = cut.Find("[data-abom-preview-held]").TextContent;
            Assert.Contains("team = Platform and tier = 1", held);
            Assert.Contains("have not been read yet", held);
        });
    }

    // A refused save says why in the server's words, which name the property and what rules it
    // out; a status code would leave the reader guessing which scope to change.
    [Fact]
    public void ARefusedSaveSaysWhy()
    {
        Serve();
        RefuseWrite("api/owned-scope", HttpStatusCode.BadRequest,
            "phi-scan can be set by repository administrators, so it cannot decide what is yours");
        var cut = Arrive();

        Lines(cut)[0].QuerySelector("[data-abom-remove-scope]")!.Click();
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Contains(
            "phi-scan can be set by repository administrators", cut.Markup));
    }

    // What a property scope let go, and what it could not judge, are reported with what came in.
    [Fact]
    public void TheEnumerationReportSaysWhatLeftAndWhatWasNotJudged()
    {
        Serve(enumeration: """
            {"added":0,"promoted":1,"unchanged":8,"failed":[],
             "accounts":[{"owner":"ops-team","found":9,"added":0,"promoted":1,"left":2,"propertiesUnjudged":3}]}
            """);
        var cut = Arrive();

        Lines(cut)[2].QuerySelector("[data-abom-remove-scope]")!.Click();
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() =>
        {
            var line = cut.Find("[data-abom-enumerated='ops-team']").TextContent;
            Assert.Contains("2 left", line);
            Assert.Contains("3 not judged", line);
        });
    }
}
