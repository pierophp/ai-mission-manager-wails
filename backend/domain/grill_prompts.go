package domain

import (
	"fmt"
	"strings"
)

func ComposeGrillContinuationPrompt(state DomainState, runID int64, action GrillContinuationAction) (string, error) {
	run, ok := runByID(state, runID)
	if !ok {
		return "", fmt.Errorf("Run %d does not exist", runID)
	}
	if run.ExecutionProfile != ExecutionProfileGrill {
		return "", fmt.Errorf("Run %d is not a Grill Run", runID)
	}
	item, ok := itemByID(state, run.ItemID)
	if !ok {
		return "", fmt.Errorf("Item %d does not exist", run.ItemID)
	}
	context := []string{"Item objective:\n" + item.Title}
	if strings.TrimSpace(item.Notes) != "" {
		context = append(context, "Item notes:\n"+strings.TrimSpace(item.Notes))
	}
	context = append(context, linkedItemSources(state, item.ID)...)
	decisions := "No structured Grill decisions were recorded."
	if len(run.GrillDecisions) > 0 {
		lines := make([]string, 0, len(run.GrillDecisions))
		for _, decision := range run.GrillDecisions {
			lines = append(lines, fmt.Sprintf("Q%d: %s", decision.QuestionNumber, strings.ReplaceAll(strings.TrimSpace(decision.Answer), "\n", "\n   ")))
		}
		decisions = strings.Join(lines, "\n")
	} else if run.GrillResponse != nil && strings.TrimSpace(*run.GrillResponse) != "" {
		decisions = *run.GrillResponse
	} else if formatted, err := FormatGrillResponse(run.GrillAnswers); err == nil {
		decisions = formatted
	}
	sections := []string{
		GrillLanguageFromPrompt(run.Prompt).responseInstruction(),
		"Continue the existing Grill Run in the same Run and Pane. The Grill conversation is already in your context; use it as the primary source.",
		"Selected downstream action: " + string(action),
		"Downstream skill snapshot (inject this content explicitly; do not rely on the agent having the skill installed):\n" + grillContinuationSkillSnapshot(action),
		"Relevant Item context:\n" + strings.Join(context, "\n\n"),
		"Recorded Grill decisions:\n" + decisions,
	}
	if open := openGrillQuestions(run); open != "" {
		if run.GrillAction == nil {
			sections = append(sections, "The user stopped the Grill early, before answering these questions. Do not answer them yourself and do not ask them again: record each one in the spec as an open question, with your recommendation.\n"+open)
		} else {
			sections = append(sections, fmt.Sprintf("The user moved on from %s without answering its last questions. Do not ask them again: treat each one as settled by its recommendation.\n%s", *run.GrillAction, open))
		}
	}
	if action == "to-tickets" {
		specs := downstreamIssueURLs(state, run.ID, "to-spec")
		if len(specs) > 0 {
			sections = append(sections, "Spec created earlier in this Run (the reference for to-tickets: fetch it and read its full body and comments, and use it as the tickets' parent):\n"+strings.Join(specs, "\n"))
		}
	}
	sections = append(sections,
		"Issue tracker configuration: read the repository's AGENTS.md or CLAUDE.md and the files they point to (such as docs/agents/issue-tracker.md and docs/agents/triage-labels.md). Only ask the user to run /setup-matt-pocock-skills when none of them configure a tracker.",
		fmt.Sprintf("Continuation instruction:\nApply the selected %s skill to the Item using the conversation and decisions above. Keep working in the same working directory. Ask any confirmation questions as one grouped frontier that follows the output contract below. Wait for an explicit user decision to finish or stop; never mark the Item Done automatically.", action),
		GrillOutputContract,
		fmt.Sprintf("Mission Manager links the work objects you create to the Item. For to-tickets, set each ticket's native parent to the Spec when the tracker supports it; treat that relation write as best-effort, so a rejection must not stop creation. Mission Manager records the parent locally and never reads tracker relations back. Immediately after creating each object, print one JSON event on a line by itself. Use its canonical URL, or its local Markdown path for files under a registered checkout. Keep objects in publication order; use a 1-based ordinal for each ticket and list any tickets it is blocked by as their URLs or paths in blocked_by. Example: AI_MISSION_MANAGER_EVENT {\"event\":\"external.object.created\",\"url\":\"<canonical URL or local path>\",\"ordinal\":1,\"blocked_by\":[],\"run_id\":%d,\"action\":\"%s\"}", run.ID, action),
	)
	return strings.Join(sections, "\n\n"), nil
}

func grillContinuationSkillSnapshot(action GrillContinuationAction) string {
	switch action {
	case "to-spec":
		return stripSkillFrontmatter(toSpecSkillDocument)
	case "to-tickets":
		return stripSkillFrontmatter(toTicketsSkillDocument)
	case "implement":
		return stripSkillFrontmatter(implementationSkillDocument)
	default:
		return ""
	}
}

func openGrillQuestions(run Run) string {
	if run.GrillPhase == nil || *run.GrillPhase != "waiting_for_answers" || run.GrillQuestionGroup == nil {
		return ""
	}
	lines := make([]string, 0, len(run.GrillQuestionGroup.Questions))
	for _, question := range run.GrillQuestionGroup.Questions {
		line := fmt.Sprintf("Q%d: %s %s", question.Number, valueOrEmpty(question.Title), strings.ReplaceAll(question.Prompt, "\n", " "))
		if question.Recommendation != nil {
			line += fmt.Sprintf(" (recommended: %s)", *question.Recommendation)
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	return strings.Join(lines, "\n")
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func downstreamIssueURLs(state DomainState, runID int64, action GrillContinuationAction) []string {
	urls := []string{}
	for _, link := range state.Links {
		if link.Provenance == nil || link.Provenance.RunID != runID || link.Provenance.Action != action {
			continue
		}
		for _, object := range state.ExternalObjects {
			if object.ID == link.ExternalObjectID {
				urls = append(urls, object.CanonicalURL)
				break
			}
		}
	}
	return urls
}

func ComposePlanGoPrompt(run Run) (string, error) {
	if run.Workflow != WorkflowPstack || run.ExecutionProfile != ExecutionProfilePlan || run.State != RunFinished || run.PaneStatus != PaneAvailable || run.PlanPhase == nil || *run.PlanPhase != "awaiting_go" {
		return "", fmt.Errorf("Plan Go is not available for Run %d", run.ID)
	}
	path := "the plan you just wrote"
	if run.PlanPath != nil {
		path = *run.PlanPath
	}
	return fmt.Sprintf("%s\n\nThe user approved continuing this Plan Run by selecting Go. Continue in this same Run, Pane, and working directory. Read and execute the plan at `%s`. Implement its phases, perform the required verification, and report the resulting changes and checks. Do not rewrite the plan unless implementation reveals a concrete blocker.", GrillLanguageFromPrompt(run.Prompt).runResponseInstruction(), path), nil
}

func (language GrillLanguage) runResponseInstruction() string {
	if language == GrillEnglish {
		return "Respond to the user in English throughout this Run. Keep code, identifiers, proper names, and quoted source text in their original language when appropriate."
	}
	return "Respond to the user in Portuguese throughout this Run. Keep code, identifiers, proper names, and quoted source text in their original language when appropriate."
}

func runByID(state DomainState, id int64) (Run, bool) {
	for _, run := range state.Runs {
		if run.ID == id {
			return run, true
		}
	}
	return Run{}, false
}
