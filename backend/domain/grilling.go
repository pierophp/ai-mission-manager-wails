package domain

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed assets/grilling-skill.md
var grillingSkillDocument string

//go:embed assets/implement-skill.md
var implementationSkillDocument string

//go:embed assets/to-spec-skill.md
var toSpecSkillDocument string

//go:embed assets/to-tickets-skill.md
var toTicketsSkillDocument string

// GrillLanguage is the language used for agent responses in one Run.
type GrillLanguage string

const (
	GrillPortuguese GrillLanguage = "portuguese"
	GrillEnglish    GrillLanguage = "english"
)

const GrillOutputContract = "Mission Manager output contract (it reads your questions from the terminal and shows them to the user as a form):\n- Start every question on its own line with `❓ **Q<n>** - **<title>**: <question>`. Number questions 1, 2, 3… within the round.\n- Put the recommendation on the line that starts with `➡️`. Do not add prose after the recommendation other than lettered options (`A) …`).\n- Separate questions with a line containing only `---`.\n- Print each round exactly once, at the end of your turn. If a sub-agent you are waiting on changes a question, print only the revised full round; Mission Manager shows only the last round printed in a turn.\n- Keep status notes (what you are checking, what you found) before the first `❓`, never between or after the questions.\n- The user answers every question of the round at once, with one numbered reply (`1. …`, `2. …`). An answer of `ok` accepts your recommendation."

func (language GrillLanguage) responseInstruction() string {
	if language == GrillEnglish {
		return "GRILL_RESPONSE_LANGUAGE=english\nRespond to the user in English throughout this Grill Run, including every answer and continuation. Keep code, identifiers, proper names, and quoted source text in their original language when appropriate."
	}
	return "GRILL_RESPONSE_LANGUAGE=portuguese\nRespond to the user in Portuguese throughout this Grill Run, including every answer and continuation. Keep code, identifiers, proper names, and quoted source text in their original language when appropriate."
}

func GrillLanguageFromPrompt(prompt string) GrillLanguage {
	if strings.Contains(prompt, "GRILL_RESPONSE_LANGUAGE=english") {
		return GrillEnglish
	}
	return GrillPortuguese
}

func ComposeGrillPrompt(state DomainState, itemID int64, configuration GrillConfiguration, language GrillLanguage, initialPrompt string) (string, error) {
	if err := validateGrill(configuration); err != nil {
		return "", err
	}
	var item *Item
	for i := range state.Items {
		if state.Items[i].ID == itemID {
			item = &state.Items[i]
			break
		}
	}
	if item == nil {
		return "", fmt.Errorf("Item %d does not exist", itemID)
	}
	initialPrompt = strings.TrimSpace(initialPrompt)
	if initialPrompt == "" {
		return "", fmt.Errorf("initial prompt cannot be blank")
	}
	context := append([]string{"Item objective:\n" + item.Title}, linkedItemSources(state, itemID)...)
	return fmt.Sprintf("You are starting a Grill Run.\n\n%s\n\nGrill configuration: agent=%s, model=%s, effort=%s.\n\nGrilling skill snapshot:\n%s\n\n%s\n\nRelevant Item context:\n%s\n\nUser's initial prompt:\n%s", language.responseInstruction(), configuration.Agent, configuration.Model, configuration.Effort, GrillSkillSnapshot(), GrillOutputContract, strings.Join(context, "\n\n"), initialPrompt), nil
}

func GrillSkillSnapshot() string {
	return stripSkillFrontmatter(grillingSkillDocument)
}

func linkedItemSources(state DomainState, itemID int64) []string {
	var sources []string
	for _, link := range state.Links {
		if link.ItemID != itemID {
			continue
		}
		for _, object := range state.ExternalObjects {
			if object.ID != link.ExternalObjectID {
				continue
			}
			title := "Linked external object"
			for _, snapshot := range state.Snapshots {
				if snapshot.ExternalObjectID == object.ID {
					title = snapshot.Title
					break
				}
			}
			sources = append(sources, fmt.Sprintf("Linked source:\n%s\n%s", title, object.CanonicalURL))
			break
		}
	}
	return sources
}

func FormatGrillResponse(answers []GrillAnswer) (string, error) {
	if len(answers) == 0 {
		return "", fmt.Errorf("Grill answers cannot be empty")
	}
	answers = append([]GrillAnswer(nil), answers...)
	sortGrillAnswers(answers)
	parts := make([]string, 0, len(answers))
	for i, answer := range answers {
		if strings.TrimSpace(answer.Answer) == "" {
			return "", fmt.Errorf("Grill answers cannot be empty")
		}
		if i > 0 && answers[i-1].QuestionNumber == answer.QuestionNumber {
			return "", fmt.Errorf("duplicate Grill answer for question %d", answer.QuestionNumber)
		}
		lines := strings.Split(strings.TrimSpace(answer.Answer), "\n")
		first := fmt.Sprintf("%d. %s", answer.QuestionNumber, strings.TrimSpace(lines[0]))
		for _, line := range lines[1:] {
			first += "\n   " + strings.TrimSpace(line)
		}
		parts = append(parts, first)
	}
	return strings.Join(parts, "\n"), nil
}

func sortGrillAnswers(answers []GrillAnswer) {
	for i := 1; i < len(answers); i++ {
		for j := i; j > 0 && answers[j].QuestionNumber < answers[j-1].QuestionNumber; j-- {
			answers[j], answers[j-1] = answers[j-1], answers[j]
		}
	}
}

type DownstreamIssueCandidate struct {
	URL       string                   `json:"url"`
	Discovery string                   `json:"discovery"`
	Ordinal   *uint                    `json:"ordinal,omitempty"`
	BlockedBy []string                 `json:"blockedBy,omitempty"`
	RunID     *int64                   `json:"runId,omitempty"`
	Action    *GrillContinuationAction `json:"action,omitempty"`
}

type externalObjectCreatedEvent struct {
	Event     string                   `json:"event"`
	Type      string                   `json:"type"`
	Kind      string                   `json:"kind"`
	URL       string                   `json:"url"`
	Ordinal   *uint                    `json:"ordinal"`
	BlockedBy []string                 `json:"blocked_by"`
	RunID     *int64                   `json:"run_id"`
	Action    *GrillContinuationAction `json:"action"`
}

var ansiEscape = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

func ParseGrillQuestionGroup(transcript string) *GrillQuestionGroup {
	return ParseGrillQuestionGroupSince("", transcript)
}

func ParseGrillQuestionGroupSince(previous, transcript string) *GrillQuestionGroup {
	previous = ansiEscape.ReplaceAllString(previous, "")
	transcript = ansiEscape.ReplaceAllString(transcript, "")
	var fence byte
	fenceLen := 0
	if strings.HasPrefix(transcript, previous) {
		for _, line := range strings.Split(previous, "\n") {
			skipGrillFenceLine(line, &fence, &fenceLen)
		}
		transcript = transcript[len(previous):]
	}
	var questions []GrillQuestion
	var current *grillQuestionDraft
	for _, line := range strings.Split(transcript, "\n") {
		if skipGrillFenceLine(line, &fence, &fenceLen) {
			continue
		}
		line = strings.TrimSpace(line)
		line, block := stripGrillAgentMarker(line)
		if strings.HasPrefix(line, "❓") {
			if current != nil {
				if q, ok := current.finish(); ok {
					questions = append(questions, q)
				}
			}
			number, title, prompt := parseGrillQuestionHeader(strings.TrimPrefix(line, "❓"), uint32(len(questions)+1))
			if len(questions) > 0 && number <= questions[len(questions)-1].Number {
				questions = nil
			}
			current = &grillQuestionDraft{question: GrillQuestion{Number: number, Title: title, Prompt: prompt, Options: []GrillOption{}}}
			continue
		}
		if current == nil {
			continue
		}
		if block || grillSeparator(line) || strings.HasPrefix(line, "#") {
			current.closed = true
			continue
		}
		current.push(line)
	}
	if current != nil {
		if q, ok := current.finish(); ok {
			questions = append(questions, q)
		}
	}
	if len(questions) == 0 {
		return nil
	}
	return &GrillQuestionGroup{Questions: questions}
}

func GrillTranscriptExtends(previous, transcript string) bool {
	previous = ansiEscape.ReplaceAllString(previous, "")
	transcript = ansiEscape.ReplaceAllString(transcript, "")
	return strings.HasPrefix(transcript, previous)
}

type grillQuestionDraft struct {
	question  GrillQuestion
	section   string
	paragraph bool
	closed    bool
}

func (d *grillQuestionDraft) push(line string) {
	if d.closed {
		return
	}
	if line == "" {
		if d.section == "prompt" {
			d.paragraph = true
		} else if d.section == "recommendation" {
			d.section = "options"
		}
		return
	}
	if strings.HasPrefix(line, "➡️") || strings.HasPrefix(line, "➡") {
		v := cleanGrillMarkup(strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(line, "➡️"), "➡"), " "))
		if v != "" {
			d.question.Recommendation = &v
			d.section = "recommendation"
		}
		return
	}
	if key, label, ok := parseGrillOption(line); ok {
		d.question.Options = append(d.question.Options, GrillOption{Key: key, Label: label})
		if d.section == "recommendation" {
			d.section = "options"
		}
		return
	}
	switch d.section {
	case "prompt":
		if d.question.Prompt != "" {
			separator := " "
			if d.paragraph {
				separator = "\n\n"
			} else if startsGrillList(line) {
				separator = "\n"
			}
			d.question.Prompt += separator
		}
		d.question.Prompt += cleanGrillMarkup(line)
		d.paragraph = false
	case "recommendation":
		if d.question.Recommendation != nil {
			v := *d.question.Recommendation + " " + cleanGrillMarkup(line)
			d.question.Recommendation = &v
		}
	}
}
func (d *grillQuestionDraft) finish() (GrillQuestion, bool) {
	q := d.question
	q.Prompt = strings.TrimSpace(q.Prompt)
	if q.Prompt == "" {
		return q, false
	}
	if q.Title == nil {
		if title, prompt, ok := splitGrillLeadingQuestion(q.Prompt); ok {
			q.Title = &title
			q.Prompt = prompt
		}
	}
	return q, true
}

func parseGrillQuestionHeader(header string, fallback uint32) (uint32, *string, string) {
	header = strings.TrimLeft(header, "︎")
	header = strings.TrimSpace(strings.ReplaceAll(header, "**", ""))
	header = strings.Trim(header, "-—–:) .")
	number, rest := parseGrillQuestionNumber(header)
	if number == 0 {
		number = fallback
		rest = header
	}
	rest = strings.TrimSpace(strings.Trim(rest, "-—–:) ."))
	if index := strings.Index(rest, ":"); index >= 0 {
		title, prompt := cleanGrillMarkup(rest[:index]), cleanGrillMarkup(rest[index+1:])
		if title != "" && prompt != "" && !strings.Contains(title, "?") && len([]rune(title)) <= 80 {
			return number, &title, prompt
		}
	}
	return number, nil, cleanGrillMarkup(rest)
}
func parseGrillQuestionNumber(s string) (uint32, string) {
	s = strings.TrimSpace(s)
	i := 0
	if strings.HasPrefix(strings.ToLower(s), "question") {
		i = 8
	} else if strings.HasPrefix(strings.ToLower(s), "q") {
		i = 1
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		return 0, s
	}
	var n uint32
	for _, c := range s[i:j] {
		n = n*10 + uint32(c-'0')
	}
	return n, s[j:]
}
func parseGrillOption(s string) (string, string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	key, rest := s[:1], s[1:]
	if s[0] == '(' {
		i := strings.IndexByte(s, ')')
		if i < 0 {
			return "", "", false
		}
		key, rest = s[1:i], s[i+1:]
	}
	if len(key) != 1 || key[0] < 'A' || key[0] > 'Z' {
		return "", "", false
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" || !strings.ContainsRune(".) :-", rune(rest[0])) {
		return "", "", false
	}
	label := cleanGrillMarkup(strings.TrimSpace(rest[1:]))
	return key, label, label != ""
}
func cleanGrillMarkup(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "**", ""), "*", ""))
}
func grillSeparator(s string) bool {
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 3 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("-—–─━_* ", r) {
			return false
		}
	}
	return true
}
func stripGrillAgentMarker(s string) (string, bool) {
	for _, mark := range []string{"•", "⏺", "●", "└"} {
		if strings.HasPrefix(s, mark) {
			return strings.TrimLeft(s[len(mark):], " \t"), true
		}
	}
	return s, false
}
func startsGrillList(s string) bool {
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "• ") {
		return true
	}
	digitEnd := 0
	for digitEnd < len(s) && s[digitEnd] >= '0' && s[digitEnd] <= '9' {
		digitEnd++
	}
	return digitEnd > 0 && strings.HasPrefix(s[digitEnd:], ". ")
}
func splitGrillLeadingQuestion(s string) (string, string, bool) {
	i := strings.Index(s, "?")
	if i < 0 {
		return "", "", false
	}
	title, rest := strings.TrimSpace(s[:i+1]), strings.TrimSpace(s[i+1:])
	if len([]rune(title)) > 120 || rest == "" || strings.Contains(title, "\n") {
		return "", "", false
	}
	return title, rest, true
}
func skipGrillFenceLine(line string, marker *byte, length *int) bool {
	t := strings.TrimLeft(line, " \t")
	if t == "" {
		return *marker != 0
	}
	b := t[0]
	if b != '`' && b != '~' {
		return *marker != 0
	}
	n := 0
	for n < len(t) && t[n] == b {
		n++
	}
	if n < 3 {
		return *marker != 0
	}
	tail := strings.TrimSpace(t[n:])
	if *marker == 0 {
		*marker = b
		*length = n
	} else if b == *marker && n >= *length && tail == "" {
		*marker = 0
		*length = 0
	}
	return true
}

func DiscoverDownstreamIssueCandidates(output string) []DownstreamIssueCandidate {
	candidates := []DownstreamIssueCandidate{}
	var ordinal uint
	for _, line := range strings.Split(output, "\n") {
		line, _ = stripGrillAgentMarker(strings.TrimSpace(line))
		raw := strings.TrimSpace(strings.TrimPrefix(line, "AI_MISSION_MANAGER_EVENT "))
		raw = strings.Trim(raw, "`")
		if strings.HasPrefix(raw, "{") {
			var e externalObjectCreatedEvent
			if json.Unmarshal([]byte(raw), &e) == nil {
				kind := e.Event
				if kind == "" {
					kind = e.Type
				}
				if kind == "" {
					kind = e.Kind
				}
				if (kind == "external.object.created" || kind == "github.issue.created") && e.URL != "" {
					o := e.Ordinal
					if o == nil {
						ordinal++
						o = &ordinal
					} else if *o > ordinal {
						ordinal = *o
					}
					candidates = append(candidates, DownstreamIssueCandidate{URL: e.URL, Discovery: "structured-event", Ordinal: o, BlockedBy: e.BlockedBy, RunID: e.RunID, Action: e.Action})
				}
			}
		}
		for _, token := range strings.Fields(line) {
			url := strings.Trim(token, "()[]{}<>\"'`,;:!?")
			url = strings.TrimRight(url, ".,;!?")
			url = strings.TrimSuffix(url, "/")
			if isDownstreamCaptureReference(url) {
				ordinal++
				v := ordinal
				candidates = append(candidates, DownstreamIssueCandidate{URL: url, Discovery: "output-url", Ordinal: &v})
			}
		}
	}
	out := []DownstreamIssueCandidate{}
	for _, candidate := range candidates {
		found := false
		for _, existing := range out {
			if strings.EqualFold(existing.URL, candidate.URL) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, candidate)
		}
	}
	return out
}

func isDownstreamCaptureReference(reference string) bool {
	if strings.HasPrefix(reference, "https://") {
		object, err := ClassifyExternalURL(reference)
		return err == nil && object.Provider != ProviderGeneric
	}
	path := strings.TrimPrefix(reference, "file://")
	if !(strings.HasPrefix(path, "/") || strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") || strings.HasPrefix(path, ".scratch/")) {
		return false
	}
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".md" || extension == ".markdown"
}
