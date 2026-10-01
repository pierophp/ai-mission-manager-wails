package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

const (
	codexCatalogKey       = "grill_codex_model_catalog"
	codexCatalogErrorKey  = "grill_codex_model_catalog_error"
	codexCatalogTTL       = 24 * time.Hour
	catalogRetryDelay     = 5 * time.Minute
	catalogCommandTimeout = 20 * time.Second
)

type CatalogRefreshStatus string

const (
	CatalogReady      CatalogRefreshStatus = "ready"
	CatalogRefreshing CatalogRefreshStatus = "refreshing"
	CatalogError      CatalogRefreshStatus = "error"
)

type GrillModelCatalogSnapshot struct {
	Catalogs       []domain.GrillAgentCatalog `json:"catalogs"`
	CodexStatus    CatalogRefreshStatus       `json:"codexStatus"`
	CodexError     *string                    `json:"codexError"`
	CodexFetchedAt *int64                     `json:"codexFetchedAt"`
}

type codexCatalogCache struct {
	FetchedAt int64               `json:"fetchedAt"`
	Models    []domain.GrillModel `json:"models"`
}

var claudeEffortsOnce sync.Once
var cachedClaudeEfforts []domain.GrillEffort

func (r *Runtime) listGrillModelCatalog() (GrillModelCatalogSnapshot, error) {
	serialized, err := r.store.Setting(codexCatalogKey)
	if err != nil {
		return GrillModelCatalogSnapshot{}, fmt.Errorf("read Codex model catalog: %w", err)
	}
	var cached *codexCatalogCache
	if serialized != "" {
		var value codexCatalogCache
		if json.Unmarshal([]byte(serialized), &value) == nil {
			cached = &value
		}
	}
	errorMessage, err := r.store.Setting(codexCatalogErrorKey)
	if err != nil {
		return GrillModelCatalogSnapshot{}, fmt.Errorf("read Codex model catalog error: %w", err)
	}
	stale := cached == nil || time.Now().Unix()-cached.FetchedAt >= int64(codexCatalogTTL.Seconds())
	if stale {
		r.startCatalogRefresh(false)
	}
	r.backgroundMu.Lock()
	refreshing := r.catalogRefreshing
	r.backgroundMu.Unlock()
	status := CatalogReady
	var catalogError *string
	switch {
	case refreshing:
		status = CatalogRefreshing
	case stale:
		status = CatalogError
		if strings.TrimSpace(errorMessage) == "" {
			errorMessage = "Codex model discovery is unavailable"
		}
		catalogError = stringPtr(errorMessage)
	}
	catalogs := make([]domain.GrillAgentCatalog, 0, 2)
	claude := claudeCatalog()
	if len(claude.Models) > 0 {
		catalogs = append(catalogs, claude)
	}
	var fetchedAt *int64
	if cached != nil {
		fetchedAt = int64Ptr(cached.FetchedAt)
		catalogs = append(catalogs, domain.GrillAgentCatalog{Agent: domain.AgentCodex, Models: cached.Models})
	}
	return GrillModelCatalogSnapshot{Catalogs: catalogs, CodexStatus: status, CodexError: catalogError, CodexFetchedAt: fetchedAt}, nil
}

func (r *Runtime) refreshGrillModelCatalog() { r.startCatalogRefresh(true) }

func (r *Runtime) startCatalogRefresh(force bool) {
	if r == nil || r.store == nil {
		return
	}
	now := time.Now().Unix()
	r.backgroundMu.Lock()
	if r.catalogRefreshing || (!force && now-r.catalogLastAttempt < int64(catalogRetryDelay.Seconds())) {
		r.backgroundMu.Unlock()
		return
	}
	r.catalogRefreshing = true
	r.catalogLastAttempt = now
	r.backgroundMu.Unlock()
	go func() {
		models, err := discoverCodexCatalog()
		if err != nil {
			_ = r.store.SetSettings(map[string]string{codexCatalogErrorKey: err.Error()})
		} else if encoded, marshalErr := json.Marshal(codexCatalogCache{FetchedAt: time.Now().Unix(), Models: models}); marshalErr != nil {
			_ = r.store.SetSettings(map[string]string{codexCatalogErrorKey: "Could not save the Codex model catalog"})
		} else {
			_ = r.store.SetSettings(map[string]string{codexCatalogKey: string(encoded), codexCatalogErrorKey: ""})
		}
		r.backgroundMu.Lock()
		r.catalogRefreshing = false
		r.backgroundMu.Unlock()
	}()
}

func discoverCodexCatalog() ([]domain.GrillModel, error) {
	executable := resolveExecutable("codex", "")
	if executable == "" {
		return nil, errors.New("Codex CLI was not found on PATH")
	}
	output, err := runCatalogCommand(executable, "debug", "models")
	if err != nil {
		return nil, err
	}
	models, err := parseCodexModelCatalog(output)
	if err != nil {
		return nil, err
	}
	return models, nil
}

func runCatalogCommand(executable string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), catalogCommandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, args...).Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, errors.New("Provider model discovery timed out")
	}
	if err != nil {
		return nil, errors.New("Provider CLI could not discover its model catalog")
	}
	return output, nil
}

func parseCodexModelCatalog(payload []byte) ([]domain.GrillModel, error) {
	var response struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
			Default     string `json:"default_reasoning_level"`
			Levels      []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal(payload, &response) != nil {
		return nil, errors.New("Codex CLI returned an invalid model catalog")
	}
	if response.Models == nil {
		return nil, errors.New("Codex CLI returned no model catalog")
	}
	result := make([]domain.GrillModel, 0, len(response.Models))
	for _, model := range response.Models {
		if strings.EqualFold(model.Visibility, "hidden") || model.Slug == "" {
			continue
		}
		efforts := make([]domain.GrillEffort, 0, len(model.Levels))
		for _, level := range model.Levels {
			if level.Effort != "" {
				efforts = append(efforts, domain.GrillEffort{ID: level.Effort, Label: effortLabel(level.Effort)})
			}
		}
		if len(efforts) == 0 {
			continue
		}
		if model.Default != "" {
			for index := range efforts {
				if efforts[index].ID == model.Default {
					efforts[0], efforts[index] = efforts[index], efforts[0]
					break
				}
			}
		}
		label := model.DisplayName
		if strings.TrimSpace(label) == "" {
			label = model.Slug
		}
		result = append(result, domain.GrillModel{ID: model.Slug, Label: label, Efforts: efforts})
	}
	if len(result) == 0 {
		return nil, errors.New("Codex CLI returned no usable models")
	}
	return result, nil
}

func claudeCatalog() domain.GrillAgentCatalog {
	claudeEffortsOnce.Do(func() {
		path := resolveExecutable("claude", "")
		if path == "" {
			return
		}
		output, err := runCatalogCommand(path, "--help")
		if err == nil {
			cachedClaudeEfforts = parseClaudeEfforts(string(output))
		}
	})
	models := domain.GrillModelCatalog()[0].Models
	if len(cachedClaudeEfforts) == 0 {
		models = []domain.GrillModel{}
		return domain.GrillAgentCatalog{Agent: domain.AgentClaude, Models: models}
	}
	for i := range models {
		models[i].Efforts = append([]domain.GrillEffort{}, cachedClaudeEfforts...)
	}
	return domain.GrillAgentCatalog{Agent: domain.AgentClaude, Models: models}
}

func parseClaudeEfforts(help string) []domain.GrillEffort {
	flattened := strings.Join(strings.Fields(help), " ")
	option := strings.Index(flattened, "--effort <level>")
	if option < 0 {
		return []domain.GrillEffort{}
	}
	section := flattened[option:]
	open := strings.IndexByte(section, '(')
	if open < 0 {
		return []domain.GrillEffort{}
	}
	close := strings.IndexByte(section[open+1:], ')')
	if close < 0 {
		return []domain.GrillEffort{}
	}
	var efforts []domain.GrillEffort
	for _, id := range strings.Split(section[open+1:open+1+close], ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			efforts = append(efforts, domain.GrillEffort{ID: id, Label: effortLabel(id)})
		}
	}
	if efforts == nil {
		return []domain.GrillEffort{}
	}
	return efforts
}

func effortLabel(id string) string {
	switch id {
	case "xhigh":
		return "Extra high"
	case "ultra":
		return "Ultra"
	}
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + id[1:]
}
