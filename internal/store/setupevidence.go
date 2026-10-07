package store

import (
	"fmt"
	"slices"
	"strings"
)

// setupEvidence says, in one sentence, what put a repository in its class.
func setupEvidence(c SetupClass) string {
	languages := strings.Join(c.Languages, ", ")
	switch c.Class {
	case SetupNotOwned:
		return "outside the declared scope, so ABOM does not read it"
	case SetupNotListed:
		return "missing from the last full listing since " + c.MissingAt + ": archived, deleted, renamed or moved"
	case SetupConflict:
		return "default setup is on for " + languages + ", and " + advancedEvidence(c)
	case SetupAdvanced:
		return advancedEvidence(c)
	case SetupUnreadable:
		return "GitHub refused the read: " + c.Unreadable
	case SetupNotRead:
		switch {
		case c.SetupReadAt == "":
			return "default setup has not been read yet"
		case !c.ActionsCode && !c.CodeKnown:
			return "no listing has said yet whether it holds Actions code"
		case c.ConfigurationsUnread != "":
			return "the account's code security configurations could not be read: " + c.ConfigurationsUnread
		}
		return "the account's code security configurations have not been read"
	case SetupCovered:
		return "default setup analyzes " + languages
	case SetupNoActionsCode:
		return "no .github/workflows, .github/actions or root action.yml, and GitHub detects no Actions code"
	case SetupNotCrawled:
		return fmt.Sprintf("ABOM has not analyzed its workflows (%s), so advanced setup cannot be ruled out",
			orNone(c.Crawl, "never queued"))
	case SetupNotDetected:
		return "default setup is off and GitHub would analyze " + orNone(languages, "nothing") +
			", though the listing found Actions code"
	case SetupBlocked:
		return "the enforced configuration " + c.ConfigurationName +
			" turns default setup off, and GitHub refuses a change to it there"
	case SetupActionsNotAllowed:
		return c.ActionsPolicy
	case SetupLanguageNotWritable:
		return "default setup analyzes " + strings.Join(unwritable(c.Languages), ", ") +
			", which GitHub's write refuses, so actions cannot be added without removing it"
	case SetupAdd:
		if c.AttachmentStatus == "enforced" {
			return "default setup analyzes " + orNone(languages, "nothing") + " without actions, under the enforced configuration " +
				c.ConfigurationName + ", which adding a language keeps enforced"
		}
		return "default setup analyzes " + orNone(languages, "nothing") + " without actions"
	case SetupEnable:
		return "default setup is off; GitHub would analyze " + languages
	}
	return "default setup reported state " + orNone(c.State, "none")
}

func advancedEvidence(c SetupClass) string {
	if c.ReachesCodeQL {
		return "a workflow it runs uses github/codeql-action"
	}
	return "CodeQL analysis " + c.AdvancedKey + " at " + c.AdvancedAt + " was not made by default setup"
}

func orNone(v, none string) string {
	if v == "" {
		return none
	}
	return v
}

// unwritable lists the languages read that a write cannot send back.
func unwritable(languages []string) []string {
	out := []string{}
	for _, l := range languages {
		if !slices.Contains(SetupWriteLanguages, l) && l != "javascript" && l != "typescript" {
			out = append(out, l)
		}
	}
	return out
}
