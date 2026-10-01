package backend

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type GrillEffort = domain.GrillEffort

func equalUsageWindows(left, right []UsageWindow) bool { return reflect.DeepEqual(left, right) }
func equalEfforts(left, right []GrillEffort) bool      { return reflect.DeepEqual(left, right) }

func usageFailureFrom(err error) *usageFailure {
	var failure usageFailure
	if errors.As(err, &failure) {
		return &failure
	}
	return nil
}

func TestParseClaudeUsageReportsProviderWindowsAndExtraUsage(t *testing.T) {
	got, err := parseClaudeUsage(`{"cachedUsageUtilization":{"fetchedAtMs":1790709887000,"utilization":{"five_hour":{"utilization":0.42,"resets_at":1790731127},"seven_day":{"utilization":0.1,"resets_at":1791203784},"seven_day_sonnet":{"utilization":0.9,"resets_at":1791203784},"extra_usage":{"is_enabled":true,"utilization":66}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.observedAt == nil || *got.observedAt != 1790709887 {
		t.Fatalf("observedAt = %v, want 1790709887", got.observedAt)
	}
	want := []UsageWindow{
		{ID: "five_hour", Label: "5-hour window", UsedPercent: 42, ResetsAt: int64Pointer(1790731127)},
		{ID: "seven_day", Label: "Weekly", UsedPercent: 10, ResetsAt: int64Pointer(1791203784)},
		{ID: "seven_day_sonnet", Label: "Weekly (Sonnet)", UsedPercent: 90, ResetsAt: int64Pointer(1791203784)},
		{ID: "extra_usage", Label: "Extra usage", UsedPercent: 66},
	}
	if !equalUsageWindows(got.windows, want) {
		t.Fatalf("windows = %#v, want %#v", got.windows, want)
	}
}

func TestParseClaudeUsageDistinguishesMissingAndSignedOutState(t *testing.T) {
	if _, err := parseClaudeUsage(" \n"); err == nil || usageFailureFrom(err) == nil || usageFailureFrom(err).state != ProfileUsageSignedOut {
		t.Fatalf("empty Claude state error = %#v, want signed out", err)
	}
	if _, err := parseClaudeUsage(`{"projects":{}}`); err == nil || usageFailureFrom(err) == nil || usageFailureFrom(err).state != ProfileUsageNotReported {
		t.Fatalf("uncached Claude state error = %#v, want not reported", err)
	}
}

func TestParseCodexUsageUsesReportedWindowDurations(t *testing.T) {
	got, err := parseCodexUsage("not json\n" + `{"id":2,"result":{"rateLimits":{"planType":"plus","primary":{"usedPercent":2,"windowDurationMins":300,"resetsAt":1790731127},"secondary":{"usedPercent":8,"windowDurationMins":10080,"resetsAt":1791203785}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.plan == nil || *got.plan != "plus" {
		t.Fatalf("plan = %v, want plus", got.plan)
	}
	want := []UsageWindow{
		{ID: "primary", Label: "5-hour window", UsedPercent: 2, ResetsAt: int64Pointer(1790731127)},
		{ID: "secondary", Label: "Weekly", UsedPercent: 8, ResetsAt: int64Pointer(1791203785)},
	}
	if !equalUsageWindows(got.windows, want) {
		t.Fatalf("windows = %#v, want %#v", got.windows, want)
	}
}

func TestParseCodexUsageClassifiesErrorsAsSignedOut(t *testing.T) {
	_, err := parseCodexUsage(`{"id":2,"error":{"code":-32000,"message":"Not logged in"}}`)
	if err == nil || usageFailureFrom(err) == nil || usageFailureFrom(err).state != ProfileUsageSignedOut || usageFailureFrom(err).detail != "Not logged in" {
		t.Fatalf("Codex error = %#v, want signed out with provider message", err)
	}
}

func TestParseCodexModelCatalogFiltersHiddenAndPrioritizesDefaultEffort(t *testing.T) {
	got, err := parseCodexModelCatalog([]byte(`{"models":[{"slug":"gpt-a","display_name":"GPT A","default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}]},{"slug":"secret","visibility":"hidden","supported_reasoning_levels":[{"effort":"high"}]},{"slug":"no-efforts","supported_reasoning_levels":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "gpt-a" || got[0].Label != "GPT A" || len(got[0].Efforts) != 2 || got[0].Efforts[0].ID != "high" || got[0].Efforts[1].ID != "low" {
		t.Fatalf("catalog = %#v", got)
	}
}

func TestParseClaudeEffortsReadsTheHelpOption(t *testing.T) {
	got := parseClaudeEfforts("Usage: claude [options]\n  --effort <level> (low, medium, high, xhigh, max)\n")
	want := []GrillEffort{{ID: "low", Label: "Low"}, {ID: "medium", Label: "Medium"}, {ID: "high", Label: "High"}, {ID: "xhigh", Label: "Extra high"}, {ID: "max", Label: "Max"}}
	if !equalEfforts(got, want) {
		t.Fatalf("efforts = %#v, want %#v", got, want)
	}
	if got := parseClaudeEfforts("Usage: claude [options]\n"); len(got) != 0 {
		t.Fatalf("efforts without option = %#v, want empty", got)
	}
}

func TestUsagePayloadTypesMatchFrozenFrontendShape(t *testing.T) {
	profile := ProfilePlanUsage{ProfileID: 11, Provider: "claude", State: ProfileUsageReady, Windows: []UsageWindow{}}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"profileId", "profileName", "provider", "machineId", "machineName", "state", "detail", "plan", "observedAt", "windows"} {
		if _, ok := got[key]; !ok {
			t.Errorf("profile JSON omits %q: %s", key, encoded)
		}
	}
}

func TestPlanUsageDispatcherReturnsCachedSnapshotAndEmptyRefreshResult(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service := CommandService{Runtime: runtime}
	cached := PlanUsageSnapshot{Profiles: []ProfilePlanUsage{}, FetchedAt: int64Ptr(time.Now().Unix()), Status: PlanUsageReady}
	serialized, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.store.SetSettings(map[string]string{planUsageSnapshotKey: string(serialized)}); err != nil {
		t.Fatal(err)
	}
	got, err := service.Invoke("list_plan_usage", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot PlanUsageSnapshot
	if err := json.Unmarshal(got, &snapshot); err != nil {
		t.Fatalf("snapshot JSON %s: %v", got, err)
	}
	if snapshot.Status != PlanUsageReady || len(snapshot.Profiles) != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	refreshed, err := service.Invoke("refresh_plan_usage", `{}`)
	if err != nil {
		t.Fatalf("refresh command should be fire-and-forget: %v", err)
	}
	if string(refreshed) != "null" {
		t.Fatalf("refresh result = %s, want null", refreshed)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runtime.backgroundMu.Lock()
		refreshing := runtime.planUsageRefreshing
		runtime.backgroundMu.Unlock()
		if !refreshing {
			break
		}
		time.Sleep(time.Millisecond)
	}
	runtime.backgroundMu.Lock()
	refreshing := runtime.planUsageRefreshing
	runtime.backgroundMu.Unlock()
	if refreshing {
		t.Fatal("Plan Usage refresh did not finish")
	}
}

func TestPlanUsageStaleCacheRespectsRetryDelay(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	cached := PlanUsageSnapshot{Profiles: []ProfilePlanUsage{}, FetchedAt: int64Ptr(time.Now().Unix() - int64(planUsageTTL.Seconds())), Status: PlanUsageReady}
	serialized, _ := json.Marshal(cached)
	if err := runtime.store.SetSettings(map[string]string{planUsageSnapshotKey: string(serialized)}); err != nil {
		t.Fatal(err)
	}
	runtime.backgroundMu.Lock()
	runtime.planUsageLastAttempt = time.Now().Unix()
	runtime.backgroundMu.Unlock()
	got, err := runtime.listPlanUsage()
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != PlanUsageReady {
		t.Fatalf("stale cached status = %q, want cached ready while retry is throttled", got.Status)
	}
	runtime.backgroundMu.Lock()
	refreshing := runtime.planUsageRefreshing
	runtime.backgroundMu.Unlock()
	if refreshing {
		t.Fatal("stale cache started a refresh before the 60-second retry elapsed")
	}
}

func TestGrillCatalogDispatcherReads24HourCache(t *testing.T) {
	claudeEffortsOnce.Do(func() {})
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	models := []domain.GrillModel{{ID: "gpt-cache", Label: "Cached model", Efforts: []domain.GrillEffort{{ID: "high", Label: "High"}}}}
	encoded, err := json.Marshal(codexCatalogCache{FetchedAt: time.Now().Unix(), Models: models})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.store.SetSettings(map[string]string{codexCatalogKey: string(encoded)}); err != nil {
		t.Fatal(err)
	}
	service := CommandService{Runtime: runtime}
	got, err := service.Invoke("list_grill_model_catalog", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot GrillModelCatalogSnapshot
	if err := json.Unmarshal(got, &snapshot); err != nil {
		t.Fatalf("catalog JSON %s: %v", got, err)
	}
	if snapshot.CodexStatus != CatalogReady || snapshot.CodexFetchedAt == nil {
		t.Fatalf("catalog snapshot = %#v", snapshot)
	}
	found := false
	for _, catalog := range snapshot.Catalogs {
		if catalog.Agent == domain.AgentCodex && len(catalog.Models) == 1 && catalog.Models[0].ID == "gpt-cache" {
			found = true
		}
	}
	if !found {
		t.Fatalf("catalog snapshot omitted cached Codex model: %#v", snapshot.Catalogs)
	}
	noRuntime := CommandService{}
	refreshed, err := noRuntime.Invoke("refresh_grill_model_catalog", `{}`)
	if err != nil {
		t.Fatalf("refresh command should be fire-and-forget: %v", err)
	}
	if string(refreshed) != "null" {
		t.Fatalf("refresh result = %s, want null", refreshed)
	}
}

func TestGrillCatalogExpiredCacheRespectsFiveMinuteRetry(t *testing.T) {
	runtime, err := OpenRuntime(filepath.Join(t.TempDir(), "mission-manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	cache := codexCatalogCache{FetchedAt: time.Now().Unix() - int64(codexCatalogTTL.Seconds()), Models: []domain.GrillModel{{ID: "cached", Label: "Cached", Efforts: []domain.GrillEffort{{ID: "high", Label: "High"}}}}}
	encoded, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.store.SetSettings(map[string]string{codexCatalogKey: string(encoded), codexCatalogErrorKey: "previous discovery failed"}); err != nil {
		t.Fatal(err)
	}
	runtime.backgroundMu.Lock()
	runtime.catalogLastAttempt = time.Now().Unix()
	runtime.backgroundMu.Unlock()
	snapshot, err := runtime.listGrillModelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CodexStatus != CatalogError || snapshot.CodexError == nil || *snapshot.CodexError != "previous discovery failed" {
		t.Fatalf("expired catalog state = %#v", snapshot)
	}
	runtime.backgroundMu.Lock()
	refreshing := runtime.catalogRefreshing
	runtime.backgroundMu.Unlock()
	if refreshing {
		t.Fatal("expired cache started discovery before the five-minute retry elapsed")
	}
}

func int64Pointer(value int64) *int64 { return &value }
