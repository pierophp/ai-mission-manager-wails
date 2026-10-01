package domain

type GrillEffort struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type GrillModel struct {
	ID      string        `json:"id"`
	Label   string        `json:"label"`
	Efforts []GrillEffort `json:"efforts"`
}

type GrillAgentCatalog struct {
	Agent  AgentKind    `json:"agent"`
	Models []GrillModel `json:"models"`
}

func GrillModelCatalog() []GrillAgentCatalog {
	claudeEfforts := []GrillEffort{{"low", "Low"}, {"medium", "Medium"}, {"high", "High"}, {"xhigh", "Extra high"}, {"max", "Max"}}
	codexEfforts := []GrillEffort{{"low", "Low"}, {"medium", "Medium"}, {"high", "High"}, {"xhigh", "Extra high"}}
	claude := []GrillModel{
		{"claude-opus-5", "Claude Opus 5", cloneEfforts(claudeEfforts)},
		{"claude-sonnet-5", "Claude Sonnet 5", cloneEfforts(claudeEfforts)},
		{"claude-opus-4-8", "Claude Opus 4.8", cloneEfforts(claudeEfforts)},
		{"claude-sonnet-4-6", "Claude Sonnet 4.6", cloneEfforts(claudeEfforts)},
		{"claude-opus-4-5-20251101", "Claude Opus 4.5", cloneEfforts(claudeEfforts)},
		{"claude-sonnet-4-5-20250929", "Claude Sonnet 4.5", cloneEfforts(claudeEfforts)},
		{"claude-haiku-4-5-20251001", "Claude Haiku 4.5", cloneEfforts(claudeEfforts)},
		{"claude-sonnet-4-5", "Claude Sonnet 4.5 (legacy ID)", cloneEfforts(claudeEfforts)},
		{"claude-haiku-4-5", "Claude Haiku 4.5 (legacy ID)", cloneEfforts(claudeEfforts)},
	}
	return []GrillAgentCatalog{
		{Agent: AgentClaude, Models: claude},
		{Agent: AgentCodex, Models: []GrillModel{{"gpt-6-sol", "GPT-6 Sol", cloneEfforts(codexEfforts)}, {"gpt-6-luna", "GPT-6 Luna", cloneEfforts(codexEfforts)}}},
	}
}

func cloneEfforts(efforts []GrillEffort) []GrillEffort { return append([]GrillEffort{}, efforts...) }
