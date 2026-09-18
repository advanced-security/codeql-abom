using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Declaring an account says which repositories are the operator's; enumerating turns that into
/// repositories. Until 2026-09-08 only the first happened, so an operator could declare both
/// accounts they owned — 397 repositories — and be shown a corpus holding one of them, which had
/// arrived as somebody else's dependency.
/// </summary>
public class EnumerationTests : AbomTestContext
{
    private const string Identity = """
        {"authenticated":true,"login":"data-douser","host":"github.com",
         "verifiedAt":"2026-09-04T02:12:00Z","problem":null,
         "accounts":[{"owner":"data-douser","kind":"user","role":"owner","canDeclare":true},
                     {"owner":"advanced-security","kind":"organization","role":"admin",
                      "canDeclare":true}]}
        """;

    private const string Declared = """
        [{"owner":"data-douser","kind":"user","enabled":true,
          "includesOnly":false,"includes":[],"declaredAt":"2026-09-07T00:00:00Z",
          "declaredBy":"interface","state":"confirmed"}]
        """;

    private void Serve(string enumeration = """
        {"added":26,"promoted":1,"unchanged":0,
         "accounts":[{"owner":"data-douser","found":27,"added":26,"promoted":1}],"failed":[],
         "queued":27}
        """)
    {
        // Registered before `api/owned-scope`: the stub matches by prefix, in order.
        Answer("api/owned-scope/preview", """
            {"rootsNow":1,"rootsNext":2,"closureNow":1,"closureNext":2,
             "entering":[],"leaving":[],"enteringCount":0,"leavingCount":0,"truncated":false,
             "queuedNow":0,"queuedNext":0}
            """);
        Answer("api/owned-scope/enumerate", enumeration);
        Answer("api/owned-scope", Declared);
        Answer("api/owner-candidates", "[]");
        Answer("api/accounts", Identity);
    }

    // Saving a declaration must enumerate it. A separate button would let an operator do the part
    // that records an intention and skip the part that acts on it — which is the state the
    // reference corpus was found in.
    [Fact]
    public void SavingADeclarationEnumeratesIt()
    {
        Serve();
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("data-abom-save", cut.Markup));

        Declare(cut);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Contains(
            Sent, r => r.Method == "POST" && r.Url.Contains("api/owned-scope/enumerate")));
    }

    // Enumeration must follow the write, not race it: enumerating the scope the server has not
    // been told about yet crawls the previous declaration.
    [Fact]
    public void EnumerationFollowsTheWrite()
    {
        Serve();
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("data-abom-save", cut.Markup));

        Declare(cut);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Contains(
            Sent, r => r.Url.Contains("api/owned-scope/enumerate")));
        var wrote = Sent.ToList().FindIndex(r => r.Method == "PUT" && r.Url.EndsWith("api/owned-scope"));
        var enumerated = Sent.ToList().FindIndex(r => r.Url.Contains("api/owned-scope/enumerate"));
        Assert.True(wrote >= 0 && wrote < enumerated,
            $"the declaration was written at {wrote} and enumerated at {enumerated}");
    }

    // The number is the whole point of the exercise: an operator who declares an account needs to
    // be told what it turned out to hold, or a corpus of one repository looks like a correct one.
    [Fact]
    public void TheOperatorIsToldWhatTheDeclarationHeld()
    {
        Serve();
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("data-abom-save", cut.Markup));

        Declare(cut);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Contains("27", Outcome(cut)));
    }

    // An account that could not be reached must be named. A short report read as a small estate
    // is the failure this whole entry exists to correct.
    [Fact]
    public void AnAccountThatCouldNotBeReachedIsNamed()
    {
        Serve("""
            {"added":0,"promoted":0,"unchanged":0,"accounts":[],
             "failed":[{"owner":"data-douser","error":"rate limited"}]}
            """);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("data-abom-save", cut.Markup));

        Declare(cut);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Contains("data-douser", Outcome(cut)));
        Assert.Contains("rate limited", Outcome(cut));
    }

    // An empty account is not a failure, and reporting it as one would send an operator looking
    // for a fault in a correct answer.
    [Fact]
    public void AnEmptyAccountIsReportedWithoutAlarm()
    {
        Serve("""
            {"added":0,"promoted":0,"unchanged":0,
             "accounts":[{"owner":"data-douser","found":0,"added":0,"promoted":0}],"failed":[],
             "queued":0}
            """);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("data-abom-save", cut.Markup));

        Declare(cut);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Contains("data-douser", Outcome(cut)));
        Assert.DoesNotContain("could not", Outcome(cut));
    }

    // Recording a repository is not analyzing it. Until 2026-09-08 enumeration only wrote rows,
    // leaving 198 repositories in the corpus that nothing would ever read.
    [Fact]
    public void TheOperatorIsToldWhatWasQueuedForAnalysis()
    {
        Serve();
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("data-abom-save", cut.Markup));

        Declare(cut);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Contains("27 queued for analysis", Outcome(cut)));
    }

    // Queueing and recording fail separately, and a page that reports only the first would say
    // nothing happened when the declaration was saved and the repositories were written.
    [Fact]
    public void AQueueThatFailedIsNamedRatherThanCountedAsNone()
    {
        Serve("""
            {"added":26,"promoted":1,"unchanged":0,
             "accounts":[{"owner":"data-douser","found":27,"added":26,"promoted":1}],"failed":[],
             "queued":0,"queueError":"queue is locked"}
            """);
        var cut = Render<Pages.Settings>();
        cut.WaitForAssertion(() => Assert.Contains("data-abom-save", cut.Markup));

        Declare(cut);
        cut.Find("[data-abom-save]").Click();

        cut.WaitForAssertion(() => Assert.Contains("queue is locked", Outcome(cut)));
        Assert.Contains("27", Outcome(cut));
    }

    private static string Outcome(IRenderedComponent<Pages.Settings> cut) =>
        cut.Find("[data-abom-enumeration]").TextContent;

    // A save is only offered on a dirty draft, so a test that wants to save has to declare
    // something first. The membership list is collapsed until asked for, so this opens it.
    private static void Declare(IRenderedComponent<Pages.Settings> cut)
    {
        cut.WaitForAssertion(
            () => Assert.NotNull(cut.Find("[data-abom-memberships] .mud-expand-panel-header")));
        cut.Find("[data-abom-memberships] .mud-expand-panel-header").Click();
        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-declare='advanced-security']")));
        cut.Find("[data-abom-declare='advanced-security']").Click();
    }
}
