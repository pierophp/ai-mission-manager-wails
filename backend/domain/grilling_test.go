package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestGrillQuestionParserCapturesRecommendationOptionsAndLaterRounds(t *testing.T) {
	transcript := "retained prose\n❓ **Q1** - **Repository layout**: Which layout should we keep?\n➡️ **Keep the current layout**\nA) Keep current\nB. Split repositories\n---\n❓ 2. What should we document next?\n"
	group := ParseGrillQuestionGroup(transcript)
	if group == nil || len(group.Questions) != 2 {
		t.Fatalf("question group = %#v", group)
	}
	first := group.Questions[0]
	if first.Number != 1 || value(first.Title) != "Repository layout" || first.Prompt != "Which layout should we keep?" || value(first.Recommendation) != "Keep the current layout" {
		t.Fatalf("first question = %#v", first)
	}
	if !reflect.DeepEqual(first.Options, []GrillOption{{Key: "A", Label: "Keep current"}, {Key: "B", Label: "Split repositories"}}) {
		t.Fatalf("options = %#v", first.Options)
	}
	if group.Questions[1].Prompt != "What should we document next?" {
		t.Fatalf("second question = %#v", group.Questions[1])
	}

	prior := "❓ Q1: First decision?\n"
	transcript = prior + "1. answer\n❓ Q1: Later decision?\n➡️ Choose later.\n"
	group = ParseGrillQuestionGroupSince(prior, transcript)
	if group == nil || len(group.Questions) != 1 || group.Questions[0].Prompt != "Later decision?" {
		t.Fatalf("later group = %#v", group)
	}
}

func TestGrillQuestionParserIgnoresFencedAndMalformedOutput(t *testing.T) {
	if got := ParseGrillQuestionGroup("```md\n❓ Q1: Example?\n```\nordinary output"); got != nil {
		t.Fatalf("fenced output parsed: %#v", got)
	}
	if got := ParseGrillQuestionGroup("❓\n➡️\n---"); got != nil {
		t.Fatalf("malformed output parsed: %#v", got)
	}
	previous := "```markdown\nExample output\n"
	if got := ParseGrillQuestionGroupSince(previous, previous+"❓ Q1: This is still a code sample?\n"); got != nil {
		t.Fatalf("question inside retained code fence parsed: %#v", got)
	}
}

func TestFormatGrillResponseSortsAnswersAndRejectsInvalidInput(t *testing.T) {
	got, err := FormatGrillResponse([]GrillAnswer{{QuestionNumber: 2, Answer: "second\nmore"}, {QuestionNumber: 1, Answer: "first"}})
	if err != nil || got != "1. first\n2. second\n   more" {
		t.Fatalf("formatted answers = %q, %v", got, err)
	}
	if _, err := FormatGrillResponse([]GrillAnswer{{QuestionNumber: 1, Answer: "x"}, {QuestionNumber: 1, Answer: "y"}}); err == nil {
		t.Fatal("duplicate answers were accepted")
	}
}

func TestDiscoverDownstreamIssuesPreservesStructuredProvenanceAndFindsURLs(t *testing.T) {
	got := DiscoverDownstreamIssueCandidates("AI_MISSION_MANAGER_EVENT {\"event\":\"external.object.created\",\"url\":\"https://github.com/acme/app/issues/7\",\"ordinal\":2,\"blocked_by\":[\"https://github.com/acme/app/issues/6\"],\"run_id\":9,\"action\":\"to-tickets\"}\nPublished https://github.com/acme/app/issues/8.\n")
	if len(got) != 2 {
		t.Fatalf("candidates = %#v", got)
	}
	if got[0].Discovery != "structured-event" || got[0].RunID == nil || *got[0].RunID != 9 || got[0].Action == nil || *got[0].Action != "to-tickets" || got[0].Ordinal == nil || *got[0].Ordinal != 2 || !reflect.DeepEqual(got[0].BlockedBy, []string{"https://github.com/acme/app/issues/6"}) {
		t.Fatalf("structured candidate = %#v", got[0])
	}
	if got[1].URL != "https://github.com/acme/app/issues/8" || got[1].Discovery != "output-url" {
		t.Fatalf("URL candidate = %#v", got[1])
	}
}

func TestDiscoverDownstreamReferencesCoversProvidersAndLocalMarkdown(t *testing.T) {
	got := DiscoverDownstreamIssueCandidates("• AI_MISSION_MANAGER_EVENT {\"event\":\"external.object.created\",\"url\":\"https://acme.atlassian.net/browse/OPS-41\",\"ordinal\":2,\"blocked_by\":[\"https://dev.azure.com/acme/Platform/_workitems/edit/17\"],\"run_id\":9,\"action\":\"to-tickets\"}\n.scratch/spec.md\nhttps://dev.azure.com/acme/Platform/_workitems/edit/18")
	if len(got) != 3 || got[0].URL != "https://acme.atlassian.net/browse/OPS-41" || got[0].Discovery != "structured-event" || !reflect.DeepEqual(got[0].BlockedBy, []string{"https://dev.azure.com/acme/Platform/_workitems/edit/17"}) || got[1].URL != ".scratch/spec.md" || got[2].URL != "https://dev.azure.com/acme/Platform/_workitems/edit/18" {
		t.Fatalf("downstream references = %#v", got)
	}
}

func TestComposeGrillPromptIncludesObjectiveLanguageAndLinks(t *testing.T) {
	state := DomainState{Items: []Item{{ID: 1, Title: "Ship the feature"}}, Links: []Link{{ItemID: 1, ExternalObjectID: 8}}, ExternalObjects: []ExternalObject{{ID: 8, CanonicalURL: "https://github.com/acme/app/issues/1"}}, Snapshots: []ExternalSnapshot{{ExternalObjectID: 8, Title: "Source Issue"}}}
	got, err := ComposeGrillPrompt(state, 1, GrillConfiguration{Agent: AgentClaude, Model: "claude-sonnet-5", Effort: "high"}, GrillEnglish, "  Decide the API  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"GRILL_RESPONSE_LANGUAGE=english", "Item objective:\nShip the feature", "Source Issue", "https://github.com/acme/app/issues/1", "User's initial prompt:\nDecide the API", "Mission Manager output contract"} {
		if !contains(got, expected) {
			t.Errorf("prompt omitted %q", expected)
		}
	}
}

func value(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func contains(s, part string) bool { return strings.Contains(s, part) }
