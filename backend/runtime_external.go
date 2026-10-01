package backend

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type ExternalLinkAction struct {
	Link    domain.ExternalLinkView `json:"link"`
	Warning *string                 `json:"warning"`
}
type PollFailure struct {
	ExternalObjectID int64  `json:"external_object_id"`
	Error            string `json:"error"`
}
type PollResult struct {
	Refreshed int           `json:"refreshed"`
	Failures  []PollFailure `json:"failures"`
}
type ExternalObjectDeletionPreview struct {
	StateFingerprint string                              `json:"state_fingerprint"`
	Plan             domain.ExternalObjectDeletionPlan   `json:"plan"`
	Links            []ExternalObjectLinkDeletionPreview `json:"links"`
	ProviderWarning  string                              `json:"providerWarning"`
}
type ExternalObjectLinkDeletionPreview struct {
	LinkID         int64  `json:"linkId"`
	ItemID         int64  `json:"itemId"`
	ItemIdentifier string `json:"itemIdentifier"`
	ItemTitle      string `json:"itemTitle"`
}
type ExternalObjectDeletionResult struct {
	Summary ExternalObjectDeletionSummary `json:"summary"`
}
type ExternalObjectDeletionSummary struct {
	ExternalObjectID int64 `json:"externalObjectId"`
	LinkCount        int   `json:"linkCount"`
	SnapshotCount    int   `json:"snapshotCount"`
	ActivityCount    int   `json:"activityCount"`
}
type IssueDocument struct {
	Body       string     `json:"body"`
	BodyFormat string     `json:"bodyFormat"`
	SubIssues  []subIssue `json:"subIssues"`
}

func (r *Runtime) stateSnapshot() (domain.DomainState, error) {
	if r == nil || r.store == nil {
		return domain.DomainState{}, errors.New("runtime is not configured")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state, nil
}
func (r *Runtime) itemContext(itemID int64) (domain.Context, error) {
	s, _ := r.stateSnapshot()
	_, context, err := itemAndContext(s, itemID)
	return context, err
}
func (r *Runtime) objectAndContext(id int64) (domain.ExternalObject, domain.Context, error) {
	s, _ := r.stateSnapshot()
	var object domain.ExternalObject
	for _, o := range s.ExternalObjects {
		if o.ID == id {
			object = o
			break
		}
	}
	if object.ID == 0 {
		return object, domain.Context{}, fmt.Errorf("External Object %d does not exist", id)
	}
	for _, l := range s.Links {
		if l.ExternalObjectID == id {
			ctx, err := r.contextForItemState(s, l.ItemID)
			return object, ctx, err
		}
	}
	return object, domain.Context{}, fmt.Errorf("External Object %d has no Link", id)
}
func (r *Runtime) contextForItemState(s domain.DomainState, itemID int64) (domain.Context, error) {
	_, context, err := itemAndContext(s, itemID)
	return context, err
}
func itemAndContext(s domain.DomainState, itemID int64) (domain.Item, domain.Context, error) {
	for _, v := range domain.SearchItems(s, "", nil) {
		if v.Item.ID == itemID {
			for _, c := range s.Contexts {
				if c.ID == v.ContextID {
					return v.Item, c, nil
				}
			}
		}
	}
	return domain.Item{}, domain.Context{}, fmt.Errorf("Item %d does not exist", itemID)
}
func (r *Runtime) externalLinkView(linkID int64) (domain.ExternalLinkView, error) {
	s, err := r.stateSnapshot()
	if err != nil {
		return domain.ExternalLinkView{}, err
	}
	for _, v := range domain.SearchItems(s, "", nil) {
		for _, l := range v.Links {
			if l.Link.ID == linkID {
				return l, nil
			}
		}
	}
	return domain.ExternalLinkView{}, fmt.Errorf("Link %d does not exist", linkID)
}
func currentUnixSeconds() int64 { return time.Now().Unix() }
func metadataAsDomain(values []externalMetadata) []domain.ExternalMetadata {
	out := make([]domain.ExternalMetadata, 0, len(values))
	for _, v := range values {
		out = append(out, domain.ExternalMetadata{Key: v.Key, Value: v.Value})
	}
	return out
}

func (r *Runtime) prepareExternalObjectDeletion(id int64) (ExternalObjectDeletionPreview, error) {
	s, err := r.stateSnapshot()
	if err != nil {
		return ExternalObjectDeletionPreview{}, err
	}
	preview, err := domain.PrepareExternalObjectDeletion(s, id)
	if err != nil {
		return ExternalObjectDeletionPreview{}, err
	}
	links := []ExternalObjectLinkDeletionPreview{}
	for _, link := range s.Links {
		if link.ExternalObjectID != id {
			continue
		}
		item, _, e := itemAndContext(s, link.ItemID)
		if e != nil {
			return ExternalObjectDeletionPreview{}, e
		}
		links = append(links, ExternalObjectLinkDeletionPreview{LinkID: link.ID, ItemID: item.ID, ItemIdentifier: item.HumanIdentifier, ItemTitle: item.Title})
	}
	return ExternalObjectDeletionPreview{StateFingerprint: preview.StateFingerprint, Plan: preview.Plan, Links: links, ProviderWarning: "Provider-owned objects are never deleted; this only removes local External Object data."}, nil
}
func (r *Runtime) deleteExternalObject(id int64, confirmed bool, fingerprint string) (ExternalObjectDeletionResult, error) {
	s, err := r.stateSnapshot()
	if err != nil {
		return ExternalObjectDeletionResult{}, err
	}
	preview, err := domain.PrepareExternalObjectDeletion(s, id)
	if err != nil {
		return ExternalObjectDeletionResult{}, err
	}
	_, err = r.transition(domain.Event{Kind: "delete_external_object", ExternalObjectID: id, Confirmed: confirmed, StateFingerprint: fingerprint})
	if err != nil {
		return ExternalObjectDeletionResult{}, err
	}
	return ExternalObjectDeletionResult{Summary: ExternalObjectDeletionSummary{ExternalObjectID: id, LinkCount: len(preview.Plan.LinkIDs), SnapshotCount: preview.Plan.SnapshotCount, ActivityCount: preview.Plan.ActivityCount}}, nil
}
func (r *Runtime) unlinkExternalLink(id int64, confirmed bool) (domain.ExternalLinkDeletionResult, error) {
	s, err := r.stateSnapshot()
	if err != nil {
		return domain.ExternalLinkDeletionResult{}, err
	}
	var objectID int64
	for _, link := range s.Links {
		if link.ID == id {
			objectID = link.ExternalObjectID
			break
		}
	}
	if objectID == 0 {
		return domain.ExternalLinkDeletionResult{}, fmt.Errorf("Link %d does not exist", id)
	}
	decision, err := r.transition(domain.Event{Kind: "unlink_external_link", LinkID: id, Confirmed: confirmed})
	if err != nil {
		return domain.ExternalLinkDeletionResult{}, err
	}
	return domain.ExternalLinkDeletionResult{LinkID: id, ExternalObjectID: objectID, ExternalObjectDeleted: !domainHasExternalObject(decision.State, objectID)}, nil
}
func domainHasExternalObject(s domain.DomainState, id int64) bool {
	for _, v := range s.ExternalObjects {
		if v.ID == id {
			return true
		}
	}
	return false
}

func (r *Runtime) linkExternalObject(itemID int64, rawURL string) (ExternalLinkAction, error) {
	ctx, err := r.itemContext(itemID)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	s, err := r.stateSnapshot()
	if err != nil {
		return ExternalLinkAction{}, err
	}
	object, err := r.classifyExternalForItem(s, itemID, rawURL, ctx)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	var prior *domain.ExternalObject
	for i := range s.ExternalObjects {
		if s.ExternalObjects[i].Provider == domain.ExternalProvider(object.Provider) && s.ExternalObjects[i].ExternalKey == object.Key {
			prior = &s.ExternalObjects[i]
			break
		}
	}
	var fetched *externalSnapshot
	warning := ""
	if prior == nil {
		value, e := r.fetchSnapshotForObject(s, ctx, object)
		if e != nil {
			warning = e.Error()
		} else if value != nil {
			fetched = value
		}
	}
	if mismatch := contextProviderMismatchWarning(ctx, object.URL); mismatch != "" {
		warningBase := strings.TrimSpace(strings.TrimSuffix(mismatch, " The Link was created."))
		if warning != "" && !strings.Contains(warning, warningBase) {
			warning += "; "
		}
		if !strings.Contains(warning, warningBase) {
			warning += mismatch
		} else if !strings.Contains(warning, "The Link was created") {
			warning += " The Link was created."
		}
	}
	input := domain.ExternalObject{Provider: domain.ExternalProvider(object.Provider), Kind: domain.ExternalObjectKind(object.Kind), ExternalKey: object.Key, CanonicalURL: object.URL}
	var snapshot *domain.ExternalSnapshot
	if fetched != nil {
		value := domain.ExternalSnapshot{Title: fetched.Title, State: fetched.State, Metadata: metadataAsDomain(fetched.Metadata), FetchedAt: fetched.FetchedAt}
		snapshot = &value
	}
	decision, err := r.transition(domain.Event{Kind: "link_external_object", ItemID: itemID, ExternalObject: &input, ExternalSnapshot: snapshot})
	if err != nil {
		return ExternalLinkAction{}, err
	}
	var linkID int64
	for _, link := range decision.State.Links {
		if link.ItemID == itemID {
			for _, external := range decision.State.ExternalObjects {
				if external.ID == link.ExternalObjectID && external.Provider == input.Provider && external.ExternalKey == input.ExternalKey {
					linkID = link.ID
					break
				}
			}
		}
	}
	if linkID == 0 {
		return ExternalLinkAction{}, errors.New("Link creation produced no Link")
	}
	view, err := r.externalLinkView(linkID)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	var warningPtr *string
	if warning != "" {
		warningPtr = &warning
	}
	return ExternalLinkAction{Link: view, Warning: warningPtr}, nil
}

func contextProviderMismatchWarning(context domain.Context, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.Trim(parsed.EscapedPath(), "/")
	if strings.HasSuffix(host, ".atlassian.net") && context.AtlassianSite != nil {
		configured := strings.ToLower(strings.TrimSpace(*context.AtlassianSite))
		configured = strings.TrimSuffix(strings.TrimPrefix(configured, "https://"), "/")
		configured = strings.TrimSuffix(configured, ".atlassian.net")
		if site := strings.TrimSuffix(host, ".atlassian.net"); configured != "" && site != configured {
			return fmt.Sprintf("This External Object targets a different site or organization than the identifiers configured for Context '%s'. The Link was created.", context.Name)
		}
	}
	if host == "bitbucket.org" && context.BitbucketWorkspace != nil {
		configured := strings.ToLower(strings.TrimSpace(*context.BitbucketWorkspace))
		workspace, _, _ := strings.Cut(path, "/")
		if configured != "" && workspace != "" && configured != workspace {
			return fmt.Sprintf("This External Object targets a different site or organization than the identifiers configured for Context '%s'. The Link was created.", context.Name)
		}
	}
	if host == "dev.azure.com" && context.AzureDevOpsOrganization != nil {
		configured := strings.ToLower(strings.TrimSpace(*context.AzureDevOpsOrganization))
		configured = strings.TrimSuffix(configured, "/")
		configured = strings.TrimPrefix(configured, "https://")
		if strings.Contains(configured, "/") {
			configured = pathBase(configured)
		}
		organization, _, _ := strings.Cut(path, "/")
		if configured != "" && organization != "" && configured != organization {
			return fmt.Sprintf("This External Object targets a different site or organization than the identifiers configured for Context '%s'. The Link was created.", context.Name)
		}
	}
	return ""
}

func validateProviderContextTarget(context domain.Context, object classifiedExternalObject) error {
	warning := contextProviderMismatchWarning(context, object.URL)
	if warning == "" {
		return nil
	}
	return errors.New(strings.TrimSpace(strings.TrimSuffix(warning, " The Link was created.")))
}

func pathBase(value string) string {
	_, tail, ok := strings.Cut(value, "/")
	if !ok {
		return value
	}
	if strings.Contains(tail, "/") {
		return pathBase(tail)
	}
	return tail
}

func (r *Runtime) classifyExternalForItem(state domain.DomainState, itemID int64, raw string, context domain.Context) (classifiedExternalObject, error) {
	trimmed := strings.TrimSpace(raw)
	if !looksLikeLocalMarkdown(trimmed) {
		return classifyExternalURL(trimmed)
	}
	item, _, err := itemAndContext(state, itemID)
	if err != nil {
		return classifiedExternalObject{}, err
	}
	project, ok := projectByID(state, item.ProjectID)
	if !ok {
		return classifiedExternalObject{}, fmt.Errorf("Project %d does not exist", item.ProjectID)
	}
	machine, ok := localExecutionMachine(state, context, r.machineAccess)
	if !ok {
		return classifiedExternalObject{}, errors.New("Local Markdown links require a local execution Machine")
	}
	for _, repo := range state.Repositories {
		if repo.ProjectID != project.ID {
			continue
		}
		for _, location := range state.RepositoryLocations {
			if location.RepositoryID != repo.ID || location.MachineID != machine.ID {
				continue
			}
			object, e := classifyLocalMarkdown(repo.ID, location.CheckoutPath, trimmed)
			if e == nil {
				return object, nil
			}
		}
	}
	return classifiedExternalObject{}, errors.New("Local Markdown files must be inside a registered Repository main checkout")
}

func projectByID(state domain.DomainState, id int64) (domain.Project, bool) {
	for _, project := range state.Projects {
		if project.ID == id {
			return project, true
		}
	}
	return domain.Project{}, false
}

func localExecutionMachine(state domain.DomainState, context domain.Context, access MachineAccess) (domain.Machine, bool) {
	if context.ExecutionMachineID == nil || access == nil {
		return domain.Machine{}, false
	}
	for _, machine := range state.Machines {
		if machine.ID == *context.ExecutionMachineID && access.IsLocal(machine) {
			return machine, true
		}
	}
	return domain.Machine{}, false
}

func looksLikeLocalMarkdown(raw string) bool {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return false
	}
	if strings.HasPrefix(raw, "file://") {
		raw = strings.TrimPrefix(raw, "file://")
	}
	ext := strings.ToLower(filepath.Ext(raw))
	return ext == ".md" || ext == ".markdown"
}

func (r *Runtime) localMarkdownPath(state domain.DomainState, context domain.Context, key string) (string, error) {
	reference, ok := strings.CutPrefix(key, "local:")
	if !ok {
		return "", errors.New("This local Markdown link has an invalid repository path")
	}
	idAndPath, relative, ok := strings.Cut(reference, "#")
	if !ok || relative == "" {
		return "", errors.New("This local Markdown link has an invalid repository path")
	}
	repositoryID, err := strconv.ParseInt(idAndPath, 10, 64)
	if err != nil {
		return "", errors.New("This local Markdown link has an invalid repository path")
	}
	machine, ok := localExecutionMachine(state, context, r.machineAccess)
	if !ok {
		return "", errors.New("Local Markdown tracker requires a local execution Machine")
	}
	var repository *domain.Repository
	for i := range state.Repositories {
		if state.Repositories[i].ID == repositoryID {
			repository = &state.Repositories[i]
			break
		}
	}
	if repository == nil {
		return "", errors.New("The Repository for this local Markdown link is unavailable")
	}
	if !stateProjectInContext(state, repository.ProjectID, context.ID) {
		return "", errors.New("The Repository for this local Markdown link is unavailable")
	}
	var root string
	for _, location := range state.RepositoryLocations {
		if location.RepositoryID == repositoryID && location.MachineID == machine.ID {
			root = location.CheckoutPath
			break
		}
	}
	if root == "" {
		return "", fmt.Errorf("Repository '%s' has no main checkout registered on local Machine '%s'", repository.Name, machine.Name)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || (strings.ToLower(filepath.Ext(clean)) != ".md" && strings.ToLower(filepath.Ext(clean)) != ".markdown") {
		return "", errors.New("This local Markdown link has an invalid repository path")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("Could not read Repository '%s' main checkout: %w", repository.Name, err)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("Local Markdown file '%s' is missing or unreadable in Repository '%s' main checkout: %w", relative, repository.Name, err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("The local Markdown file is outside its registered Repository checkout")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("The local Markdown file is not a regular file")
	}
	return path, nil
}

func stateProjectInContext(state domain.DomainState, projectID, contextID int64) bool {
	for _, p := range state.Projects {
		if p.ID == projectID && p.ContextID == contextID {
			return true
		}
	}
	return false
}

func (r *Runtime) fetchSnapshotForObject(state domain.DomainState, context domain.Context, object classifiedExternalObject) (*externalSnapshot, error) {
	switch object.Provider {
	case domain.ProviderGitHub:
		cli, err := newGHCLI(context.GHExecutablePath)
		if err != nil {
			return nil, err
		}
		snapshot, err := cli.fetchSnapshot(object, currentUnixSeconds())
		if err != nil {
			return nil, err
		}
		return &snapshot, nil
	case domain.ProviderAtlassian:
		snapshot, err := fetchAtlassianSnapshot(context, object, currentUnixSeconds())
		if err != nil {
			return nil, err
		}
		return &snapshot, nil
	case domain.ProviderAzureDevOps:
		snapshot, err := fetchAzureSnapshot(context, object, currentUnixSeconds())
		if err != nil {
			return nil, err
		}
		return &snapshot, nil
	case domain.ProviderGeneric:
		if strings.HasPrefix(object.Key, "local:") {
			path, err := r.localMarkdownPath(state, context, object.Key)
			if err != nil {
				return nil, err
			}
			snapshot, err := readLocalMarkdownSnapshot(path, currentUnixSeconds())
			if err != nil {
				return nil, err
			}
			return &snapshot, nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("snapshot fetching is not implemented for %s", object.Provider)
	}
}

func (r *Runtime) createGithubIssue(itemID, repositoryID int64, title, body string) (ExternalLinkAction, error) {
	s, err := r.stateSnapshot()
	if err != nil {
		return ExternalLinkAction{}, err
	}
	var repository *domain.Repository
	for i := range s.Repositories {
		if s.Repositories[i].ID == repositoryID {
			repository = &s.Repositories[i]
			break
		}
	}
	if repository == nil {
		return ExternalLinkAction{}, fmt.Errorf("Repository %d does not exist", repositoryID)
	}
	item, _, err := itemAndContext(s, itemID)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	itemProjectID := item.ProjectID
	if repository.ProjectID != itemProjectID {
		return ExternalLinkAction{}, errors.New("The selected Repository is not configured for this Item's Project")
	}
	repo := githubRepositoryName(repository.RemoteURL)
	if repo == "" {
		return ExternalLinkAction{}, errors.New("Repository remote is not a GitHub repository")
	}
	ctx, err := r.itemContext(itemID)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	cli, err := newGHCLI(ctx.GHExecutablePath)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	url, err := cli.createIssue(repo, strings.TrimSpace(title), body)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	return r.linkExternalObject(itemID, url)
}
func githubRepositoryName(remote string) string {
	remote = strings.TrimSpace(remote)
	var repoPath string
	if rest, ok := strings.CutPrefix(remote, "git@"); ok {
		host, value, found := strings.Cut(rest, ":")
		if !found || !strings.EqualFold(host, "github.com") {
			return ""
		}
		repoPath = value
	} else if rest, ok := strings.CutPrefix(remote, "ssh://"); ok {
		authority, value, found := strings.Cut(rest, "/")
		if !found || !strings.EqualFold(authority, "git@github.com") {
			return ""
		}
		repoPath = value
	} else if scheme, rest, ok := strings.Cut(remote, "://"); ok {
		if scheme != "https" && scheme != "http" && scheme != "git" {
			return ""
		}
		host, value, found := strings.Cut(rest, "/")
		if !found || (!strings.EqualFold(host, "github.com") && !strings.EqualFold(host, "www.github.com")) {
			return ""
		}
		repoPath = value
	} else {
		return ""
	}
	remote = repoPath
	remote = strings.TrimSuffix(remote, ".git")
	parts := strings.Split(strings.Trim(remote, "/"), "/")
	if len(parts) != 2 || !validGitHubOwner(parts[0]) || !validGitHubRepository(parts[1]) {
		return ""
	}
	return strings.ToLower(parts[0]) + "/" + strings.ToLower(parts[1])
}
func validGitHubOwner(value string) bool {
	if len(value) == 0 || len(value) > 39 || !isASCIIAlphaNumeric(value[0]) || !isASCIIAlphaNumeric(value[len(value)-1]) {
		return false
	}
	for i := range value {
		if !isASCIIAlphaNumeric(value[i]) && value[i] != '-' {
			return false
		}
	}
	return true
}
func validGitHubRepository(value string) bool {
	if len(value) == 0 || len(value) > 100 || !isASCIIAlphaNumeric(value[0]) {
		return false
	}
	for i := range value {
		if !isASCIIAlphaNumeric(value[i]) && value[i] != '-' && value[i] != '_' && value[i] != '.' {
			return false
		}
	}
	return true
}
func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func (r *Runtime) fetchIssueDocument(id int64) (IssueDocument, error) {
	o, c, e := r.objectAndContext(id)
	if e != nil {
		return IssueDocument{}, e
	}
	object := classifiedExternalObject{o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL}
	switch o.Provider {
	case domain.ProviderGitHub:
		if o.Kind != domain.ObjectIssue {
			return IssueDocument{}, errors.New("Only GitHub Issues can be read as an issue document")
		}
		cli, err := newGHCLI(c.GHExecutablePath)
		if err != nil {
			return IssueDocument{}, err
		}
		doc, err := cli.fetchIssueDocument(object)
		return IssueDocument{doc.Body, doc.BodyFormat, doc.SubIssues}, err
	case domain.ProviderAtlassian:
		if o.Kind != domain.ObjectIssue {
			return IssueDocument{}, errors.New("Only Jira work items can be read as an issue document")
		}
		body, err := fetchAtlassianDocument(c, object)
		return IssueDocument{Body: body, BodyFormat: "markdown", SubIssues: []subIssue{}}, err
	case domain.ProviderAzureDevOps:
		if o.Kind != domain.ObjectIssue {
			return IssueDocument{}, errors.New("Only Azure DevOps work items can be read as an issue document")
		}
		body, err := fetchAzureDocument(c, object)
		return IssueDocument{Body: body, BodyFormat: "html", SubIssues: []subIssue{}}, err
	case domain.ProviderGeneric:
		if !strings.HasPrefix(o.ExternalKey, "local:") {
			return IssueDocument{}, errors.New("Only local Markdown links have a readable issue document")
		}
		s, err := r.stateSnapshot()
		if err != nil {
			return IssueDocument{}, err
		}
		path, err := r.localMarkdownPath(s, c, o.ExternalKey)
		if err != nil {
			return IssueDocument{}, err
		}
		return readLocalMarkdownDocument(path, o.ExternalKey)
	default:
		return IssueDocument{}, fmt.Errorf("issue document reading is not implemented for %s", o.Provider)
	}
}
func (r *Runtime) fetchExternalDocument(id int64) (string, error) {
	o, c, e := r.objectAndContext(id)
	if e != nil {
		return "", e
	}
	object := classifiedExternalObject{o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL}
	switch o.Provider {
	case domain.ProviderGitHub:
		cli, err := newGHCLI(c.GHExecutablePath)
		if err != nil {
			return "", err
		}
		return cli.fetchDocument(object)
	case domain.ProviderAtlassian:
		return fetchAtlassianDocument(c, object)
	case domain.ProviderAzureDevOps:
		return fetchAzureDocument(c, object)
	case domain.ProviderGeneric:
		if !strings.HasPrefix(o.ExternalKey, "local:") {
			return "", fmt.Errorf("document reading is not implemented for %s", o.Provider)
		}
		s, err := r.stateSnapshot()
		if err != nil {
			return "", err
		}
		path, err := r.localMarkdownPath(s, c, o.ExternalKey)
		if err != nil {
			return "", err
		}
		doc, err := readLocalMarkdownDocument(path, o.ExternalKey)
		return doc.Body, err
	default:
		return "", fmt.Errorf("document reading is not implemented for %s", o.Provider)
	}
}
func (r *Runtime) fetchExternalComments(id int64) ([]externalComment, error) {
	o, c, e := r.objectAndContext(id)
	if e != nil {
		return nil, e
	}
	object := classifiedExternalObject{o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL}
	switch o.Provider {
	case domain.ProviderGitHub:
		cli, err := newGHCLI(c.GHExecutablePath)
		if err != nil {
			return nil, err
		}
		return cli.fetchComments(object)
	case domain.ProviderAtlassian:
		return fetchAtlassianComments(c, object)
	case domain.ProviderAzureDevOps:
		return fetchAzureComments(c, object)
	case domain.ProviderGeneric:
		if !strings.HasPrefix(o.ExternalKey, "local:") {
			return nil, fmt.Errorf("comment reading is not implemented for %s", o.Provider)
		}
		s, err := r.stateSnapshot()
		if err != nil {
			return nil, err
		}
		path, err := r.localMarkdownPath(s, c, o.ExternalKey)
		if err != nil {
			return nil, err
		}
		return readLocalMarkdownComments(path)
	default:
		return nil, fmt.Errorf("comment reading is not implemented for %s", o.Provider)
	}
}
func (r *Runtime) addExternalComment(linkID int64, body string) (domain.ExternalLinkView, error) {
	s, e := r.stateSnapshot()
	if e != nil {
		return domain.ExternalLinkView{}, e
	}
	var link *domain.Link
	for i := range s.Links {
		if s.Links[i].ID == linkID {
			link = &s.Links[i]
			break
		}
	}
	if link == nil {
		return domain.ExternalLinkView{}, fmt.Errorf("Link %d does not exist", linkID)
	}
	var o *domain.ExternalObject
	for i := range s.ExternalObjects {
		if s.ExternalObjects[i].ID == link.ExternalObjectID {
			o = &s.ExternalObjects[i]
			break
		}
	}
	if o == nil {
		return domain.ExternalLinkView{}, errors.New("The linked External Object does not exist")
	}
	ctx, e := r.contextForItemState(s, link.ItemID)
	if e != nil {
		return domain.ExternalLinkView{}, e
	}
	if o.Provider != domain.ProviderGitHub || o.Kind == domain.ObjectGeneric {
		return domain.ExternalLinkView{}, errors.New("Comments can be posted only to GitHub Issues and pull requests")
	}
	cli, e := newGHCLI(ctx.GHExecutablePath)
	if e != nil {
		return domain.ExternalLinkView{}, e
	}
	if e = cli.addComment(o.CanonicalURL, body); e != nil {
		return domain.ExternalLinkView{}, e
	}
	return r.externalLinkView(linkID)
}

func (r *Runtime) refreshExternalObject(id int64) (domain.ExternalSnapshot, error) {
	o, c, e := r.objectAndContext(id)
	if e != nil {
		return domain.ExternalSnapshot{}, e
	}
	s, e := r.stateSnapshot()
	if e != nil {
		return domain.ExternalSnapshot{}, e
	}
	fetchedPtr, e := r.fetchSnapshotForObject(s, c, classifiedExternalObject{o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL})
	if e != nil {
		return domain.ExternalSnapshot{}, e
	}
	if fetchedPtr == nil {
		return domain.ExternalSnapshot{}, fmt.Errorf("snapshot fetching is not implemented for %s", o.Provider)
	}
	fetched := *fetchedPtr
	snapshot := domain.ExternalSnapshot{ExternalObjectID: id, Title: fetched.Title, State: fetched.State, Metadata: metadataAsDomain(fetched.Metadata), FetchedAt: fetched.FetchedAt}
	decision, e := r.transition(domain.Event{Kind: "refresh_external_object", ExternalObjectID: id, ExternalSnapshot: &snapshot})
	if e != nil {
		return domain.ExternalSnapshot{}, e
	}
	for _, v := range decision.State.Snapshots {
		if v.ExternalObjectID == id {
			return v, nil
		}
	}
	return domain.ExternalSnapshot{}, errors.New("Refresh produced no External snapshot")
}
func (r *Runtime) pollExternalObjects() (PollResult, error) {
	s, e := r.stateSnapshot()
	if e != nil {
		return PollResult{}, e
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, l := range s.Links {
		for _, o := range s.ExternalObjects {
			if o.ID == l.ExternalObjectID && (o.Provider != domain.ProviderGeneric || strings.HasPrefix(o.ExternalKey, "local:")) && !seen[o.ID] {
				ids = append(ids, o.ID)
				seen[o.ID] = true
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := PollResult{Failures: []PollFailure{}}
	for _, id := range ids {
		snapshot, e := r.refreshExternalObject(id)
		if e != nil {
			result.Failures = append(result.Failures, PollFailure{id, e.Error()})
		} else {
			_ = snapshot
			result.Refreshed++
		}
	}
	return result, nil
}
