package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

const (
	planUsageSnapshotKey  = "plan_usage_snapshot"
	planUsageTTL          = 5 * time.Minute
	planUsageRetryDelay   = time.Minute
	claudeStateByteLimit  = 32 * 1024 * 1024
	codexAppServerWaitSec = 8
)

type PlanUsageSnapshot struct {
	Profiles  []ProfilePlanUsage     `json:"profiles"`
	FetchedAt *int64                 `json:"fetchedAt"`
	Status    PlanUsageRefreshStatus `json:"status"`
}

type PlanUsageRefreshStatus string

const (
	PlanUsageReady      PlanUsageRefreshStatus = "ready"
	PlanUsageRefreshing PlanUsageRefreshStatus = "refreshing"
	PlanUsageNever      PlanUsageRefreshStatus = "never"
)

type ProfileUsageState string

const (
	ProfileUsageReady              ProfileUsageState = "ready"
	ProfileUsageSignedOut          ProfileUsageState = "signedOut"
	ProfileUsageMachineUnreachable ProfileUsageState = "machineUnreachable"
	ProfileUsageNotReported        ProfileUsageState = "notReported"
)

type ProfilePlanUsage struct {
	ProfileID   int64             `json:"profileId"`
	ProfileName string            `json:"profileName"`
	Provider    domain.AgentKind  `json:"provider"`
	MachineID   int64             `json:"machineId"`
	MachineName string            `json:"machineName"`
	State       ProfileUsageState `json:"state"`
	Detail      *string           `json:"detail"`
	Plan        *string           `json:"plan"`
	ObservedAt  *int64            `json:"observedAt"`
	Windows     []UsageWindow     `json:"windows"`
}

type UsageWindow struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    *int64  `json:"resetsAt"`
}

type usageReading struct {
	plan       *string
	observedAt *int64
	windows    []UsageWindow
}

type usageFailure struct {
	state  ProfileUsageState
	detail string
}

func (e usageFailure) Error() string { return e.detail }

func (r *Runtime) listPlanUsage() (PlanUsageSnapshot, error) {
	serialized, err := r.store.Setting(planUsageSnapshotKey)
	if err != nil {
		return PlanUsageSnapshot{}, fmt.Errorf("read Plan Usage snapshot: %w", err)
	}
	var cached *PlanUsageSnapshot
	if serialized != "" {
		var snapshot PlanUsageSnapshot
		if json.Unmarshal([]byte(serialized), &snapshot) == nil {
			cached = &snapshot
		}
	}
	now := time.Now().Unix()
	stale := cached == nil || cached.FetchedAt == nil || now-*cached.FetchedAt >= int64(planUsageTTL.Seconds())
	if stale {
		r.startPlanUsageRefresh(false)
	}
	r.backgroundMu.Lock()
	refreshing := r.planUsageRefreshing
	r.backgroundMu.Unlock()
	if cached != nil {
		if refreshing {
			cached.Status = PlanUsageRefreshing
		}
		return *cached, nil
	}
	status := PlanUsageNever
	if refreshing {
		status = PlanUsageRefreshing
	}
	return PlanUsageSnapshot{Profiles: []ProfilePlanUsage{}, Status: status}, nil
}

func (r *Runtime) refreshPlanUsage() {
	r.startPlanUsageRefresh(true)
}

func (r *Runtime) startPlanUsageRefresh(force bool) {
	now := time.Now().Unix()
	r.backgroundMu.Lock()
	if r.planUsageRefreshing || (!force && now-r.planUsageLastAttempt < int64(planUsageRetryDelay.Seconds())) {
		r.backgroundMu.Unlock()
		return
	}
	r.planUsageRefreshing = true
	r.planUsageLastAttempt = now
	r.backgroundMu.Unlock()
	go func() {
		profiles := r.readPlanUsageProfiles()
		snapshot := PlanUsageSnapshot{Profiles: profiles, FetchedAt: int64Ptr(time.Now().Unix()), Status: PlanUsageReady}
		if serialized, err := json.Marshal(snapshot); err == nil {
			_ = r.store.SetSettings(map[string]string{planUsageSnapshotKey: string(serialized)})
		}
		r.backgroundMu.Lock()
		r.planUsageRefreshing = false
		r.backgroundMu.Unlock()
	}()
}

func (r *Runtime) readPlanUsageProfiles() []ProfilePlanUsage {
	r.mu.Lock()
	state := r.state
	access := r.machineAccess
	r.mu.Unlock()
	machines := make(map[int64]domain.Machine, len(state.Machines))
	for _, machine := range state.Machines {
		machines[machine.ID] = machine
	}
	profiles := make([]ProfilePlanUsage, 0, len(state.CLIConfigurationProfiles))
	for _, profile := range state.CLIConfigurationProfiles {
		machine, ok := machines[profile.MachineID]
		if !ok {
			continue
		}
		profiles = append(profiles, readProfilePlanUsage(machine, profile, access))
	}
	return profiles
}

func readProfilePlanUsage(machine domain.Machine, profile domain.CLIConfigurationProfile, access MachineAccess) ProfilePlanUsage {
	base := ProfilePlanUsage{
		ProfileID: profile.ID, ProfileName: profile.Name, Provider: profile.Provider,
		MachineID: machine.ID, MachineName: machine.Name, State: ProfileUsageNotReported,
		Windows: []UsageWindow{},
	}
	var reading usageReading
	var err error
	switch profile.Provider {
	case domain.AgentClaude:
		reading, err = readClaudePlanUsage(machine, profile, access)
	case domain.AgentCodex:
		reading, err = readCodexPlanUsage(machine, profile, access)
	default:
		err = usageFailure{state: ProfileUsageNotReported, detail: "Unknown agent provider"}
	}
	if err != nil {
		var failure usageFailure
		if errors.As(err, &failure) {
			base.State = failure.state
			base.Detail = stringPtr(failure.detail)
		} else {
			base.Detail = stringPtr(err.Error())
		}
		return base
	}
	base.State = ProfileUsageReady
	base.Plan = reading.plan
	base.ObservedAt = reading.observedAt
	base.Windows = reading.windows
	return base
}

func readClaudePlanUsage(machine domain.Machine, profile domain.CLIConfigurationProfile, access MachineAccess) (usageReading, error) {
	if access == nil {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: "Machine access is not configured"}
	}
	directory := shellQuote(profile.Directory)
	command := fmt.Sprintf("set -eu; directory=%s; for candidate in \"$directory/.claude.json\" \"$HOME/.claude.json\"; do if [ -f \"$candidate\" ]; then head -c %d -- \"$candidate\"; exit 0; fi; done; exit 0", directory, claudeStateByteLimit)
	output, err := access.RunShell(machine, command)
	if err != nil {
		return usageReading{}, usageFailure{state: ProfileUsageMachineUnreachable, detail: err.Error()}
	}
	return parseClaudeUsage(output)
}

func readCodexPlanUsage(machine domain.Machine, profile domain.CLIConfigurationProfile, access MachineAccess) (usageReading, error) {
	if access == nil {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: "Machine access is not configured"}
	}
	executable, err := access.FindExecutable(machine, "codex")
	if err != nil {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: err.Error()}
	}
	command := fmt.Sprintf("set -eu; export CODEX_HOME=%s; { printf '%%s\\n' '%s'; printf '%%s\\n' '%s'; printf '%%s\\n' '%s'; sleep %d; } | %s app-server 2>/dev/null", shellQuote(profile.Directory), codexInitialize, codexInitialized, codexReadRateLimits, codexAppServerWaitSec, shellQuote(executable))
	output, err := access.RunShell(machine, command)
	if err != nil {
		return usageReading{}, usageFailure{state: ProfileUsageMachineUnreachable, detail: err.Error()}
	}
	return parseCodexUsage(output)
}

func parseClaudeUsage(payload string) (usageReading, error) {
	if strings.TrimSpace(payload) == "" {
		return usageReading{}, usageFailure{state: ProfileUsageSignedOut, detail: "Claude Code has not been run with this profile yet"}
	}
	var state map[string]any
	if json.Unmarshal([]byte(payload), &state) != nil {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: "Claude Code state could not be read"}
	}
	cached, ok := state["cachedUsageUtilization"].(map[string]any)
	if !ok {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: "Claude Code has not recorded plan usage for this profile yet"}
	}
	utilization, ok := cached["utilization"].(map[string]any)
	if !ok {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: "Claude Code reported no plan usage"}
	}
	windows := make([]UsageWindow, 0, 5)
	for _, def := range claudeUsageWindows {
		window, ok := utilization[def.id].(map[string]any)
		if !ok {
			continue
		}
		fraction, ok := window["utilization"].(float64)
		if !ok {
			continue
		}
		windows = append(windows, UsageWindow{ID: def.id, Label: def.label, UsedPercent: clampPercent(fraction * 100), ResetsAt: numberInt64(window["resets_at"])})
	}
	if extra, ok := utilization["extra_usage"].(map[string]any); ok && extra["is_enabled"] == true {
		if percent, ok := extra["utilization"].(float64); ok {
			windows = append(windows, UsageWindow{ID: "extra_usage", Label: "Extra usage", UsedPercent: clampPercent(percent)})
		}
	}
	if len(windows) == 0 {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: "Claude Code reported no plan limits for this profile"}
	}
	return usageReading{observedAt: divideInt64(numberInt64(cached["fetchedAtMs"]), 1000), windows: windows}, nil
}

type claudeUsageWindow struct{ id, label string }

var claudeUsageWindows = []claudeUsageWindow{
	{id: "five_hour", label: "5-hour window"},
	{id: "seven_day", label: "Weekly"},
	{id: "seven_day_opus", label: "Weekly (Opus)"},
	{id: "seven_day_sonnet", label: "Weekly (Sonnet)"},
}

func parseCodexUsage(payload string) (usageReading, error) {
	var reply map[string]any
	for _, line := range strings.Split(payload, "\n") {
		var message map[string]any
		if json.Unmarshal([]byte(line), &message) == nil && numberInt64(message["id"]) != nil && *numberInt64(message["id"]) == 2 {
			reply = message
			break
		}
	}
	if reply == nil {
		return usageReading{}, usageFailure{state: ProfileUsageSignedOut, detail: "Codex did not report plan usage; the profile may not be signed in"}
	}
	if errValue, ok := reply["error"].(map[string]any); ok {
		message, _ := errValue["message"].(string)
		if message == "" {
			message = "Codex refused to report plan usage"
		}
		return usageReading{}, usageFailure{state: ProfileUsageSignedOut, detail: message}
	}
	result, _ := reply["result"].(map[string]any)
	limits, _ := result["rateLimits"].(map[string]any)
	if limits == nil {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: "Codex reported no plan limits"}
	}
	windows := make([]UsageWindow, 0, 2)
	for _, key := range []string{"primary", "secondary"} {
		window, _ := limits[key].(map[string]any)
		if window == nil {
			continue
		}
		used, ok := window["usedPercent"].(float64)
		if !ok {
			continue
		}
		windows = append(windows, UsageWindow{ID: key, Label: codexWindowLabel(numberInt64(window["windowDurationMins"])), UsedPercent: clampPercent(used), ResetsAt: numberInt64(window["resetsAt"])})
	}
	if len(windows) == 0 {
		return usageReading{}, usageFailure{state: ProfileUsageNotReported, detail: "Codex reported no plan limits"}
	}
	return usageReading{plan: stringValue(limits["planType"]), windows: windows}, nil
}

const (
	codexInitialize     = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"ai-mission-manager","version":"1"}}}`
	codexInitialized    = `{"jsonrpc":"2.0","method":"initialized","params":{}}`
	codexReadRateLimits = `{"jsonrpc":"2.0","id":2,"method":"account/rateLimits/read","params":{}}`
)

func codexWindowLabel(minutes *int64) string {
	if minutes == nil {
		return "Plan limit"
	}
	switch {
	case *minutes == 300:
		return "5-hour window"
	case *minutes == 10080:
		return "Weekly"
	case *minutes%1440 == 0:
		return fmt.Sprintf("%d-day window", *minutes/1440)
	case *minutes%60 == 0:
		return fmt.Sprintf("%d-hour window", *minutes/60)
	default:
		return fmt.Sprintf("%d-minute window", *minutes)
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func numberInt64(value any) *int64 {
	number, ok := value.(float64)
	if !ok || number != float64(int64(number)) {
		return nil
	}
	result := int64(number)
	return &result
}

func divideInt64(value *int64, divisor int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value / divisor
	return &result
}

func stringValue(value any) *string {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	return &text
}

func int64Ptr(value int64) *int64 { return &value }
