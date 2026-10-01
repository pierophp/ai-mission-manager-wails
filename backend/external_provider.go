package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

// classifiedExternalObject is the canonical identity derived from a pasted URL.
type classifiedExternalObject struct {
	Provider domain.ExternalProvider   `json:"provider"`
	Kind     domain.ExternalObjectKind `json:"kind"`
	Key      string                    `json:"external_key"`
	URL      string                    `json:"canonical_url"`
}

func classifyExternalURL(raw string) (classifiedExternalObject, error) {
	object, err := domain.ClassifyExternalURL(raw)
	if err != nil {
		return classifiedExternalObject{}, err
	}
	return classifiedExternalObject{Provider: object.Provider, Kind: object.Kind, Key: object.ExternalKey, URL: object.CanonicalURL}, nil
}

type ghCLI struct{ executable string }

func newGHCLI(configured *string) (ghCLI, error) {
	if configured != nil && strings.TrimSpace(*configured) != "" {
		path := strings.TrimSpace(*configured)
		if absolute, err := filepath.Abs(path); err == nil && filepath.IsAbs(path) {
			if info, e := os.Stat(absolute); e == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				return ghCLI{absolute}, nil
			}
		}
	}
	p, err := exec.LookPath("gh")
	if err != nil {
		return ghCLI{}, errors.New("GitHub CLI (`gh`) could not be found at the Context path or on PATH")
	}
	return ghCLI{p}, nil
}
func (g ghCLI) run(args ...string) ([]byte, error) {
	out, err := exec.Command(g.executable, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := strings.TrimSpace(string(ee.Stderr))
			if msg == "" {
				msg = "the command returned a non-zero exit status"
			}
			return nil, fmt.Errorf("GitHub CLI failed: %s", msg)
		}
		return nil, fmt.Errorf("could not run GitHub CLI: %w", err)
	}
	return out, nil
}

type ghSnapshot struct {
	Number    int               `json:"number"`
	Title     string            `json:"title"`
	State     string            `json:"state"`
	Author    json.RawMessage   `json:"author"`
	Labels    []json.RawMessage `json:"labels"`
	Milestone json.RawMessage   `json:"milestone"`
	CreatedAt string            `json:"createdAt"`
	UpdatedAt string            `json:"updatedAt"`
}
type externalMetadata struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type externalSnapshot struct {
	ExternalObjectID int64              `json:"external_object_id"`
	Title            string             `json:"title"`
	State            string             `json:"state"`
	Metadata         []externalMetadata `json:"metadata"`
	FetchedAt        int64              `json:"fetched_at"`
}

func (g ghCLI) fetchSnapshot(o classifiedExternalObject, fetchedAt int64) (externalSnapshot, error) {
	if o.Provider != "github" || (o.Kind != "issue" && o.Kind != "pull_request") {
		return externalSnapshot{}, errors.New("GitHub snapshot requires an Issue or pull request")
	}
	cmd := "issue"
	if o.Kind == "pull_request" {
		cmd = "pr"
	}
	b, err := g.run(cmd, "view", o.URL, "--json", "number,title,state,author,labels,milestone,createdAt,updatedAt")
	if err != nil {
		return externalSnapshot{}, err
	}
	var v ghSnapshot
	if err := json.Unmarshal(b, &v); err != nil {
		return externalSnapshot{}, fmt.Errorf("GitHub returned invalid JSON: %w", err)
	}
	if v.Number < 1 || v.Title == "" || v.State == "" {
		return externalSnapshot{}, errors.New("GitHub returned an Issue without its required number, title, or state")
	}
	metadata := []externalMetadata{{"number", strconv.Itoa(v.Number)}}
	if author := metadataString(v.Author); author != "" {
		metadata = append(metadata, externalMetadata{"author", author})
	}
	labels := make([]string, 0, len(v.Labels))
	for _, raw := range v.Labels {
		var label struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &label) == nil && label.Name != "" {
			labels = append(labels, label.Name)
		}
	}
	if len(labels) > 0 {
		metadata = append(metadata, externalMetadata{"labels", strings.Join(labels, ", ")})
	}
	if title := metadataObjectTitle(v.Milestone); title != "" {
		metadata = append(metadata, externalMetadata{"milestone", title})
	}
	if v.CreatedAt != "" {
		metadata = append(metadata, externalMetadata{"created", v.CreatedAt})
	}
	if v.UpdatedAt != "" {
		metadata = append(metadata, externalMetadata{"updated", v.UpdatedAt})
	}
	return externalSnapshot{Title: v.Title, State: v.State, Metadata: metadata, FetchedAt: fetchedAt}, nil
}
func metadataString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) == nil {
		if x, ok := v["login"].(string); ok {
			return x
		}
		if x, ok := v["name"].(string); ok {
			return x
		}
	}
	return string(raw)
}
func metadataObjectTitle(raw json.RawMessage) string {
	var v struct {
		Title string `json:"title"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Title
}

type issueDocument struct {
	Body       string     `json:"body"`
	BodyFormat string     `json:"bodyFormat"`
	SubIssues  []subIssue `json:"subIssues"`
}
type subIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	URL    string `json:"url"`
}
type externalComment struct {
	ID        int64  `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

func (g ghCLI) fetchDocument(o classifiedExternalObject) (string, error) {
	cmd := "issue"
	if o.Kind == "pull_request" {
		cmd = "pr"
	}
	if o.Provider != "github" || (o.Kind != "issue" && o.Kind != "pull_request") {
		return "", errors.New("only GitHub Issues and pull requests have a readable document")
	}
	b, e := g.run(cmd, "view", o.URL, "--json", "body")
	if e != nil {
		return "", e
	}
	var v struct {
		Body string `json:"body"`
	}
	e = json.Unmarshal(b, &v)
	return v.Body, e
}
func (g ghCLI) fetchIssueDocument(o classifiedExternalObject) (issueDocument, error) {
	if o.Kind != "issue" {
		return issueDocument{}, errors.New("only GitHub Issues can be read as an issue document")
	}
	body, e := g.fetchDocument(o)
	if e != nil {
		return issueDocument{}, e
	}
	repoNum := strings.TrimPrefix(o.Key, "issue:")
	repo, num, ok := strings.Cut(repoNum, "#")
	if !ok {
		return issueDocument{}, errors.New("invalid GitHub Issue identifier")
	}
	data, e := g.run("api", "repos/"+repo+"/issues/"+num+"/sub_issues", "--paginate", "--jq", ".[] | {number, title, state, html_url}")
	if e != nil {
		return issueDocument{}, e
	}
	subs := []subIssue{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var s struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			State  string `json:"state"`
			URL    string `json:"html_url"`
		}
		if e = json.Unmarshal([]byte(line), &s); e != nil {
			return issueDocument{}, e
		}
		c, _ := classifyExternalURL(s.URL)
		if c.Provider == "github" {
			s.URL = c.URL
		}
		subs = append(subs, subIssue{s.Number, s.Title, s.State, s.URL})
	}
	return issueDocument{body, "markdown", subs}, nil
}
func (g ghCLI) fetchComments(o classifiedExternalObject) ([]externalComment, error) {
	if o.Provider != "github" || (o.Kind != "issue" && o.Kind != "pull_request") {
		return nil, errors.New("comments are only available for GitHub Issues and pull requests")
	}
	key := strings.TrimPrefix(o.Key, "issue:")
	if o.Kind == "pull_request" {
		key = strings.TrimPrefix(o.Key, "pull:")
	}
	repo, num, ok := strings.Cut(key, "#")
	if !ok {
		return nil, errors.New("invalid GitHub Issue identifier")
	}
	raw, e := g.run("api", "repos/"+repo+"/issues/"+num+"/comments", "--paginate", "--slurp")
	if e != nil {
		return nil, e
	}
	var pages []json.RawMessage
	if e = json.Unmarshal(raw, &pages); e != nil {
		return nil, fmt.Errorf("GitHub returned invalid JSON: %w", e)
	}
	out := []externalComment{}
	for _, page := range pages {
		var entries []struct {
			ID   int64 `json:"id"`
			User *struct {
				Login string `json:"login"`
			} `json:"user"`
			Body    *string `json:"body"`
			Created *string `json:"created_at"`
		}
		if e = json.Unmarshal(page, &entries); e != nil {
			return nil, e
		}
		for _, c := range entries {
			if c.ID < 1 || c.Body == nil || c.Created == nil {
				return nil, errors.New("GitHub returned an incomplete comment")
			}
			author := ""
			if c.User != nil {
				author = c.User.Login
			} else {
				author = "Unknown"
			}
			out = append(out, externalComment{c.ID, author, *c.Body, *c.Created})
		}
	}
	return out, nil
}
func (g ghCLI) addComment(url, body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("a GitHub comment cannot be blank")
	}
	_, e := g.run("issue", "comment", url, "--body", body)
	return e
}
func (g ghCLI) createIssue(repository, title, body string) (string, error) {
	title = strings.TrimSpace(title)
	if strings.TrimSpace(repository) == "" || strings.TrimSpace(title) == "" {
		return "", errors.New("a GitHub repository and nonblank Issue title are required")
	}
	b, e := g.run("api", "repos/"+repository+"/issues", "--method", "POST", "--raw-field", "title="+title, "--raw-field", "body="+body)
	if e != nil {
		return "", e
	}
	var v struct {
		HTMLURL string `json:"html_url"`
	}
	if e = json.Unmarshal(b, &v); e != nil {
		return "", e
	}
	o, e := classifyExternalURL(v.HTMLURL)
	if e != nil || o.Provider != "github" || o.Kind != "issue" {
		return "", errors.New("GitHub API returned a URL that is not a GitHub Issue")
	}
	return o.URL, nil
}
