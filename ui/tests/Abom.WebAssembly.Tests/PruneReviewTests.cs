using System.Net;
using Bunit;
using MudBlazor;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Removing what is no longer owned is asked for, reviewed and then applied, in that order. It
/// opened by itself on Settings with numbers from startup, and "Remove from view" then removed
/// whatever the scope said when it was pressed, which can be far more than was shown: a review
/// is read when it is opened, applied only as reviewed, and most of the graph is never removed
/// unless that is said outright.
/// </summary>
public class PruneReviewTests : AbomTestContext
{
    private const string Removes = """
        {"declared":2,"reachable":10,"removed":{"repositories":3,"nodes":40,"edges":12,"queued":0},
         "tombstoned":0,"applied":false,"unavailable":"","live":{"nodes":400,"edges":300},
         "majority":false,"proof":"P1","checkouts":["stranger-thing"]}
        """;

    private const string RemovesMost = """
        {"declared":1,"reachable":2,"removed":{"repositories":180,"nodes":380,"edges":290,"queued":0},
         "tombstoned":0,"applied":false,"unavailable":"","live":{"nodes":400,"edges":300},
         "majority":true,"proof":"P9","checkouts":[]}
        """;

    private const string Applied = """
        {"declared":2,"reachable":10,"removed":{"repositories":3,"nodes":40,"edges":12,"queued":0},
         "tombstoned":40,"applied":true,"unavailable":"","live":{"nodes":360,"edges":288},
         "majority":false,"checkouts":[]}
        """;

    private IRenderedComponent<Shared.PruneControl>? _control;

    private IRenderedComponent<MudDialogProvider> Show(string prune)
    {
        Answer("api/owned-scope", """[{"owner":"octo-org","kind":"organization","enabled":true}]""");
        Answer("api/prune", prune);
        var dialogs = Render<MudDialogProvider>();
        _control = Render<Shared.PruneControl>();
        return dialogs;
    }

    private IRenderedComponent<Shared.PruneControl> Control() => _control!;

    // MudBlazor puts a checkbox's attributes on its input.
    private static AngleSharp.Dom.IElement Ack(IRenderedComponent<MudDialogProvider> dialogs)
    {
        var ack = dialogs.Find("[data-abom-prune-majority-ack]");
        return ack.LocalName == "input" ? ack : ack.QuerySelector("input")!;
    }

    private int PreviewReads => Sent.Count(r => r.Method == "GET" && r.Url.EndsWith("api/prune", StringComparison.Ordinal));

    private IEnumerable<SentRequest> Applies => Sent.Where(r => r.Method == "POST" && r.Url.Contains("api/prune/apply", StringComparison.Ordinal));

    [Fact]
    public void NothingOpensUntilSomebodyAsks()
    {
        var dialogs = Show(Removes);

        Control().WaitForElement("[data-abom-prune-summary]");
        Assert.Empty(dialogs.FindAll("[data-abom-prune-review-summary]"));
        Assert.Empty(Applies);
    }

    [Fact]
    public void AReviewIsReadWhenItIsOpenedAndSaysWhatGoesAsAShare()
    {
        var dialogs = Show(Removes);
        Control().WaitForElement("[data-abom-prune-review]");
        var before = PreviewReads;

        Control().Find("[data-abom-prune-review]").Click();

        var summary = dialogs.WaitForElement("[data-abom-prune-review-summary]").TextContent;
        Assert.Equal(before + 1, PreviewReads);
        Assert.Contains("40 nodes and 12 edges across 3 repositories", summary);
        Assert.Contains("10%", summary);
        Assert.Contains("stranger-thing", dialogs.Find("[data-abom-prune-review-checkouts]").TextContent);
        Assert.Empty(Applies);
    }

    [Fact]
    public void ConfirmingAppliesExactlyThePreviewReviewed()
    {
        AnswerWrite("api/prune/apply", Applied);
        var dialogs = Show(Removes);
        Control().WaitForElement("[data-abom-prune-review]").Click();

        dialogs.WaitForElement("[data-abom-prune-mark]").Click();

        Control().WaitForAssertion(() => Assert.Single(Applies));
        Assert.Contains("\"proof\":\"P1\"", Applies.Single().Body);
        Control().WaitForAssertion(() => Assert.Contains("Removed 40 nodes and 12 edges from view",
            Control().Find("[data-abom-prune-outcome]").TextContent));
    }

    // The scope can shrink between reading and pressing, as a `gh` check that cannot see an
    // organization's membership makes it: the server refuses, and the review shows what is true now.
    [Fact]
    public void AScopeThatChangedIsShownAndNothingIsClaimedRemoved()
    {
        RespondWrite("api/prune/apply", HttpStatusCode.Conflict, """
            {"error":"Nothing was removed: since it was reviewed, what you own changed or could not be confirmed, and 1 repository it kept would go: stranger/thing.",
             "view":{"declared":1,"reachable":2,"removed":{"repositories":180,"nodes":380,"edges":290,"queued":0},
                     "tombstoned":0,"applied":false,"unavailable":"","live":{"nodes":400,"edges":300},
                     "majority":true,"proof":"P2","checkouts":[]}}
            """);
        var dialogs = Show(Removes);
        Control().WaitForElement("[data-abom-prune-review]").Click();
        dialogs.WaitForElement("[data-abom-prune-mark]").Click();

        var changed = dialogs.WaitForElement("[data-abom-prune-changed]").TextContent;
        Assert.Contains("stranger/thing", changed);
        Assert.Contains("380 nodes", dialogs.Find("[data-abom-prune-review-summary]").TextContent);
        Assert.Empty(Control().FindAll("[data-abom-prune-outcome]"));

        // What it now says is most of the graph, so it waits to be said outright before P2 is sent.
        Assert.True(dialogs.Find("[data-abom-prune-mark]").HasAttribute("disabled"));
        Ack(dialogs).Change(true);
        dialogs.Find("[data-abom-prune-mark]").Click();
        Control().WaitForAssertion(() => Assert.Equal(2, Applies.Count()));
        Assert.Contains("\"proof\":\"P2\"", Applies.Last().Body);
        Assert.Contains("\"majority\":true", Applies.Last().Body);
    }

    [Fact]
    public void RemovingMostOfTheGraphIsSaidOutrightFirst()
    {
        AnswerWrite("api/prune/apply", Applied);
        var dialogs = Show(RemovesMost);
        Control().WaitForElement("[data-abom-prune-review]").Click();

        dialogs.WaitForElement("[data-abom-prune-majority]");
        Assert.Contains("95%", dialogs.Find("[data-abom-prune-review-summary]").TextContent);
        Assert.True(dialogs.Find("[data-abom-prune-mark]").HasAttribute("disabled"));

        Ack(dialogs).Change(true);
        dialogs.Find("[data-abom-prune-mark]").Click();

        Control().WaitForAssertion(() => Assert.Single(Applies));
        Assert.Contains("\"majority\":true", Applies.Single().Body);
    }

    [Fact]
    public void CancellingRemovesNothing()
    {
        var dialogs = Show(Removes);
        Control().WaitForElement("[data-abom-prune-review]").Click();

        dialogs.WaitForElement("[data-abom-prune-cancel]").Click();

        dialogs.WaitForAssertion(() => Assert.Empty(dialogs.FindAll("[data-abom-prune-review-summary]")));
        Assert.Empty(Applies);
    }

    [Fact]
    public void DeletingPermanentlyAsksFirst()
    {
        AnswerWrite("api/prune/compact", """
            {"declared":0,"reachable":0,"removed":{"repositories":0,"nodes":40,"edges":12,"queued":0},
             "tombstoned":0,"applied":true,"unavailable":"","live":{"nodes":0,"edges":0},"majority":false,"checkouts":[]}
            """);
        var dialogs = Show(Removes.Replace("\"tombstoned\":0", "\"tombstoned\":40"));
        Control().WaitForElement("[data-abom-prune-compact]").Click();

        Assert.Contains("40", dialogs.WaitForElement("[data-abom-prune-compact-summary]").TextContent);
        Assert.DoesNotContain(Sent, r => r.Method == "POST" && r.Url.Contains("api/prune/compact", StringComparison.Ordinal));

        dialogs.Find("[data-abom-prune-compact-confirm]").Click();
        Control().WaitForAssertion(() => Assert.Contains(Sent,
            r => r.Method == "POST" && r.Url.Contains("api/prune/compact", StringComparison.Ordinal)));
    }

    // Queued work is not in view, so a removal of only that has no share of the graph to state.
    [Fact]
    public void QueuedWorkAloneStatesNoShareOfTheGraph()
    {
        var dialogs = Show("""
            {"declared":33,"reachable":134,"removed":{"repositories":0,"nodes":0,"edges":0,"queued":195},
             "tombstoned":0,"applied":false,"unavailable":"","live":{"nodes":1623,"edges":909},
             "majority":false,"proof":"P3","checkouts":[]}
            """);
        Control().WaitForElement("[data-abom-prune-review]").Click();

        var summary = dialogs.WaitForElement("[data-abom-prune-review-summary]").TextContent;
        Assert.Contains("195 queued dependencies", summary);
        Assert.DoesNotContain("%", summary);
    }

    // A removal whose answer never came may have happened; saying "nothing was removed" would be a
    // claim nobody can make. What is outside the scope now is read again instead.
    [Fact]
    public void AnApplyThatGetsNoAnswerDoesNotSayNothingWasRemoved()
    {
        TimeOut("api/prune/apply");
        var dialogs = Show(Removes);
        Control().WaitForElement("[data-abom-prune-review]").Click();
        var reads = PreviewReads;

        dialogs.WaitForElement("[data-abom-prune-mark]").Click();

        var outcome = Control().WaitForElement("[data-abom-prune-outcome]").TextContent;
        Assert.Contains("not known", outcome);
        Assert.DoesNotContain("Nothing was removed", outcome);
        Control().WaitForAssertion(() => Assert.True(PreviewReads > reads, "what is left was not read again"));
    }

    // The server says why in its own words; a bare status says nothing a reader can act on. A 4xx is
    // a refusal before anything changed.
    [Fact]
    public void ARefusedApplySaysWhatTheServerSaid()
    {
        RefuseWrite("api/prune/apply", HttpStatusCode.Forbidden, "cross-site requests cannot control ABOM");
        var dialogs = Show(Removes);
        Control().WaitForElement("[data-abom-prune-review]").Click();

        dialogs.WaitForElement("[data-abom-prune-mark]").Click();

        var outcome = Control().WaitForElement("[data-abom-prune-outcome]").TextContent;
        Assert.Contains("refused", outcome);
        Assert.Contains("cross-site requests cannot control ABOM", outcome);
    }

    // A 5xx can come after removing began, so it is never called a refusal, which would say nothing
    // changed. The server's words say what may have happened, and what is left is read again.
    [Fact]
    public void AnApplyTheServerFailedPartWayIsNotCalledARefusal()
    {
        RefuseWrite("api/prune/apply", HttpStatusCode.InternalServerError,
            "removing what is outside your scope failed, and part of it may already be out of view: disk I/O error");
        var dialogs = Show(Removes);
        Control().WaitForElement("[data-abom-prune-review]").Click();
        var reads = PreviewReads;

        dialogs.WaitForElement("[data-abom-prune-mark]").Click();

        var outcome = Control().WaitForElement("[data-abom-prune-outcome]").TextContent;
        Assert.Contains("part of it may already be out of view: disk I/O error.", outcome);
        Assert.Contains("read again", outcome);
        Assert.DoesNotContain("refused", outcome);
        Assert.DoesNotContain("Nothing was removed", outcome);
        Control().WaitForAssertion(() => Assert.True(PreviewReads > reads, "what is left was not read again"));
    }

    [Fact]
    public void ADeletionTheServerFailedIsNotCalledARefusal()
    {
        RefuseWrite("api/prune/compact", HttpStatusCode.InternalServerError, "compacting: disk I/O error");
        var dialogs = Show(Removes.Replace("\"tombstoned\":0", "\"tombstoned\":40"));
        Control().WaitForElement("[data-abom-prune-compact]").Click();

        dialogs.WaitForElement("[data-abom-prune-compact-confirm]").Click();

        var outcome = Control().WaitForElement("[data-abom-prune-outcome]").TextContent;
        Assert.Contains("compacting: disk I/O error.", outcome);
        Assert.Contains("read again", outcome);
        Assert.DoesNotContain("refused", outcome);
    }

    [Fact]
    public void ADeletionThatGetsNoAnswerDoesNotSayNothingWasDeleted()
    {
        TimeOut("api/prune/compact");
        var dialogs = Show(Removes.Replace("\"tombstoned\":0", "\"tombstoned\":40"));
        Control().WaitForElement("[data-abom-prune-compact]").Click();

        dialogs.WaitForElement("[data-abom-prune-compact-confirm]").Click();

        var outcome = Control().WaitForElement("[data-abom-prune-outcome]").TextContent;
        Assert.Contains("not known", outcome);
        Assert.DoesNotContain("Nothing was deleted", outcome);
    }
}
