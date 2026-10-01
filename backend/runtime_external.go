package backend

import (
	"errors"
	"fmt"
	"sort"
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

func (r *Runtime) linkExternalObject(itemID int64, rawURL string) (ExternalLinkAction, error) {
	object, err := classifyExternalURL(rawURL)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	ctx, err := r.itemContext(itemID)
	if err != nil {
		return ExternalLinkAction{}, err
	}
	s, err := r.stateSnapshot()
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
	if prior == nil && object.Provider == "github" {
		cli, e := newGHCLI(ctx.GHExecutablePath)
		if e != nil {
			warning = e.Error()
		} else {
			value, e := cli.fetchSnapshot(object, currentUnixSeconds())
			if e != nil {
				warning = e.Error()
			} else {
				fetched = &value
			}
		}
	} else if prior == nil && object.Provider != "generic" {
		warning = fmt.Sprintf("The %s provider is classified but is not available in this build.", object.Provider)
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
	if o.Provider != domain.ProviderGitHub || o.Kind != domain.ObjectIssue {
		return IssueDocument{}, errors.New("Only GitHub Issues can be read as an issue document")
	}
	cli, e := newGHCLI(c.GHExecutablePath)
	if e != nil {
		return IssueDocument{}, e
	}
	doc, e := cli.fetchIssueDocument(classifiedExternalObject{o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL})
	return IssueDocument{doc.Body, doc.BodyFormat, doc.SubIssues}, e
}
func (r *Runtime) fetchExternalDocument(id int64) (string, error) {
	o, c, e := r.objectAndContext(id)
	if e != nil {
		return "", e
	}
	if o.Provider != domain.ProviderGitHub {
		return "", fmt.Errorf("document reading is not implemented for %s", o.Provider)
	}
	cli, e := newGHCLI(c.GHExecutablePath)
	if e != nil {
		return "", e
	}
	return cli.fetchDocument(classifiedExternalObject{o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL})
}
func (r *Runtime) fetchExternalComments(id int64) ([]externalComment, error) {
	o, c, e := r.objectAndContext(id)
	if e != nil {
		return nil, e
	}
	if o.Provider != domain.ProviderGitHub {
		return nil, fmt.Errorf("comment reading is not implemented for %s", o.Provider)
	}
	cli, e := newGHCLI(c.GHExecutablePath)
	if e != nil {
		return nil, e
	}
	return cli.fetchComments(classifiedExternalObject{o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL})
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
		return domain.ExternalLinkView{}, errors.New("Comments are only supported for GitHub Issues and pull requests")
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
	if o.Provider != domain.ProviderGitHub {
		return domain.ExternalSnapshot{}, fmt.Errorf("snapshot fetching is not implemented for %s", o.Provider)
	}
	cli, e := newGHCLI(c.GHExecutablePath)
	if e != nil {
		return domain.ExternalSnapshot{}, e
	}
	fetched, e := cli.fetchSnapshot(classifiedExternalObject{o.Provider, o.Kind, o.ExternalKey, o.CanonicalURL}, currentUnixSeconds())
	if e != nil {
		return domain.ExternalSnapshot{}, e
	}
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
			if o.ID == l.ExternalObjectID && o.Provider == domain.ProviderGitHub && !seen[o.ID] {
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
