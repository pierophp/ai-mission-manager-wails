package domain

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed assets/implement-skill.md
var implementSkillSource string

func RunLaunchOptionsFor(state DomainState, itemID int64, target RunLaunchTargetKind) (RunLaunchOptions, error) {
	item, ok := itemByID(state, itemID)
	if !ok {
		return RunLaunchOptions{}, DomainError(fmt.Sprintf("Item %d does not exist", itemID))
	}
	project, ok := projectByID(state, item.ProjectID)
	if !ok {
		return RunLaunchOptions{}, DomainError(fmt.Sprintf("Project %d does not exist", item.ProjectID))
	}
	context, ok := contextByID(state, project.ContextID)
	if !ok {
		return RunLaunchOptions{}, DomainError(fmt.Sprintf("Context %d does not exist", project.ContextID))
	}
	if target != RunTargetCheckout && target != RunTargetWorktree {
		return RunLaunchOptions{}, DomainError(fmt.Sprintf("unknown Run launch target %q", target))
	}
	workflows := make([]RunLaunchWorkflowOptions, 0, 2)
	for _, workflow := range []Workflow{WorkflowMattPocock, WorkflowPstack} {
		profiles := make([]RunLaunchProfileOption, 0, 8)
		for _, profile := range []ExecutionProfile{ExecutionProfileGrill, ExecutionProfileInvestigate, ExecutionProfileImplement, ExecutionProfileReview, ExecutionProfileAutonomous, ExecutionProfilePlan, ExecutionProfilePstackReview, ExecutionProfileCustomPrompt} {
			if !WorkflowOffers(workflow, profile) || target == RunTargetWorktree && profile == ExecutionProfileGrill {
				continue
			}
			configuration := context.ImplementDefaults
			if workflow == WorkflowPstack {
				configuration = context.PstackDefaults
			} else if profile == ExecutionProfileGrill {
				configuration = context.GrillDefaults
			}
			profiles = append(profiles, RunLaunchProfileOption{ExecutionProfile: profile, Configuration: configuration, RequiresInitialPrompt: profile == ExecutionProfileCustomPrompt || profile == ExecutionProfilePstackReview || profile == ExecutionProfileGrill})
		}
		defaultProfile := ExecutionProfileGrill
		if workflow == WorkflowPstack {
			defaultProfile = ExecutionProfileAutonomous
		}
		if workflow == WorkflowMattPocock && target == RunTargetWorktree {
			defaultProfile = ExecutionProfileInvestigate
		}
		workflows = append(workflows, RunLaunchWorkflowOptions{Workflow: workflow, DefaultProfile: defaultProfile, Profiles: profiles})
	}
	defaultWorkflow := context.DefaultWorkflow
	if defaultWorkflow != WorkflowPstack {
		defaultWorkflow = WorkflowMattPocock
	}
	return RunLaunchOptions{DefaultWorkflow: defaultWorkflow, Workflows: workflows}, nil
}

func WorkflowOffers(workflow Workflow, profile ExecutionProfile) bool {
	if workflow == WorkflowMattPocock {
		return profile == ExecutionProfileGrill || profile == ExecutionProfileInvestigate || profile == ExecutionProfileImplement || profile == ExecutionProfileReview || profile == ExecutionProfileCustomPrompt
	}
	if workflow == WorkflowPstack {
		return profile == ExecutionProfileAutonomous || profile == ExecutionProfilePlan || profile == ExecutionProfilePstackReview || profile == ExecutionProfileCustomPrompt
	}
	return false
}

func ComposeRunPrompt(state DomainState, itemID int64, profile ExecutionProfile, selection RunPromptSelection, language, initialPrompt *string, workflow Workflow) (string, error) {
	if workflow == WorkflowPstack {
		return composePstackPrompt(state, itemID, profile, language, initialPrompt)
	}
	item, ok := itemByID(state, itemID)
	if !ok {
		return "", DomainError(fmt.Sprintf("Item %d does not exist", itemID))
	}
	initial := ""
	if initialPrompt != nil {
		initial = strings.TrimSpace(*initialPrompt)
	}
	sections := make([]string, 0, len(selection.ExternalObjectIDs)+3)
	if selection.IncludeObjective {
		sections = append(sections, "Item objective:\n"+item.Title)
	}
	for _, objectID := range selection.ExternalObjectIDs {
		var linked bool
		for _, link := range state.Links {
			if link.ItemID == itemID && link.ExternalObjectID == objectID {
				linked = true
				break
			}
		}
		if !linked {
			return "", DomainError(fmt.Sprintf("external object %d is not linked to Item %d", objectID, itemID))
		}
		object, found := externalObjectByID(state, objectID)
		if !found {
			return "", DomainError(fmt.Sprintf("external object %d does not exist", objectID))
		}
		title := "Linked external object"
		for _, snapshot := range state.Snapshots {
			if snapshot.ExternalObjectID == objectID {
				title = snapshot.Title
				break
			}
		}
		sections = append(sections, "Linked source:\n"+title+"\n"+object.CanonicalURL)
	}
	var instruction string
	switch profile {
	case ExecutionProfileInvestigate:
		instruction = "Investigate this work, inspect the relevant code, and report findings before changing files."
	case ExecutionProfileImplement:
		instruction = strings.TrimSpace(stripSkillFrontmatter(implementSkillSource)) + "\n\nImplement this work using the Repositories configured for its Project or a registered Worktree, run the relevant checks, and leave the changes ready for review."
	case ExecutionProfileReview:
		instruction = "Review the current changes for this Item for correctness, regressions, and missing test coverage."
	case ExecutionProfileCustomPrompt:
		if initial == "" {
			return "", DomainError("a Run prompt cannot be blank")
		}
		instruction = initial
	case ExecutionProfileGrill:
		instruction = "Use the selected grilling skill to ask a structured frontier of questions before recommending the next decision."
	default:
		return "", DomainError(fmt.Sprintf("execution profile %q is not offered by workflow %q", profile, workflow))
	}
	sections = append([]string{instruction}, sections...)
	if profile != ExecutionProfileCustomPrompt && initial != "" {
		sections = append(sections, "User's initial prompt:\n"+initial)
	}
	if language != nil {
		sections = append([]string{runResponseInstruction(*language)}, sections...)
	}
	prompt := strings.TrimSpace(strings.Join(sections, "\n\n"))
	if prompt == "" {
		return "", DomainError("a Run prompt cannot be blank")
	}
	return prompt, nil
}

func stripSkillFrontmatter(source string) string {
	if !strings.HasPrefix(source, "---\n") {
		return source
	}
	body := strings.TrimPrefix(source, "---\n")
	end := strings.Index(body, "\n---\n")
	if end < 0 {
		return source
	}
	return body[end+len("\n---\n"):]
}

func runResponseInstruction(language string) string {
	if language == "english" {
		return "Respond to the user in English throughout this Run. Keep code, identifiers, proper names, and quoted source text in their original language when appropriate."
	}
	return "Respond to the user in Portuguese throughout this Run. Keep code, identifiers, proper names, and quoted source text in their original language when appropriate."
}

func composePstackPrompt(state DomainState, itemID int64, profile ExecutionProfile, language, initial *string) (string, error) {
	item, ok := itemByID(state, itemID)
	if !ok {
		return "", DomainError(fmt.Sprintf("Item %d does not exist", itemID))
	}
	if initial != nil && strings.TrimSpace(*initial) == "" {
		initial = nil
	}
	if (profile == ExecutionProfilePstackReview || profile == ExecutionProfileCustomPrompt) && initial == nil {
		return "", DomainError("a Run prompt cannot be blank")
	}
	instruction := "You are starting an Autonomous pstack Run. Read the pstack poteto-mode skill before acting."
	switch profile {
	case ExecutionProfilePlan:
		instruction = "You are starting a Plan pstack Run. Complete the planning phases, write the plan in the repository, then stop without implementing it. Report the plan's repository-relative path in your final response. Do not delegate implementation."
	case ExecutionProfilePstackReview:
		instruction = "You are starting a pstack Review Run. Review only: do not edit files, commit, push, or apply suggestions."
	case ExecutionProfileCustomPrompt:
		instruction = "You are starting a Custom pstack Run. No skill is selected: do what the Initial Prompt asks."
	case ExecutionProfileAutonomous:
	default:
		return "", DomainError(fmt.Sprintf("execution profile %q is not offered by workflow %q", profile, WorkflowPstack))
	}
	sections := []string{instruction, "Item: " + item.Title}
	if initial != nil {
		sections = append(sections, "Initial Prompt:\n"+strings.TrimSpace(*initial))
	}
	for _, link := range state.Links {
		if link.ItemID == itemID && link.Purpose == "to-spec" {
			if object, found := externalObjectByID(state, link.ExternalObjectID); found {
				sections = append(sections, "Spec: "+object.CanonicalURL)
				break
			}
		}
	}
	if language != nil {
		sections = append([]string{runResponseInstruction(*language) + "\nWrite commits and pull requests in English."}, sections...)
	}
	return strings.Join(sections, "\n\n"), nil
}
