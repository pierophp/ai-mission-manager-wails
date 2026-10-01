package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type providerCLI struct {
	provider   string
	executable string
	context    domain.Context
}

func newProviderCLI(provider string, configured *string, context domain.Context) (providerCLI, error) {
	name := "twg"
	if provider == "azure_dev_ops" {
		name = "az"
	}
	path := ""
	if configured != nil {
		path = strings.TrimSpace(*configured)
	}
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err == nil {
			if info, statErr := os.Stat(absolute); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				return providerCLI{provider, absolute, context}, nil
			}
		}
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		if provider == "azure_dev_ops" {
			return providerCLI{}, errors.New("Azure CLI (`az`) could not be found at the Context path or on PATH. Set its executable path in Settings → Contexts → Providers, or install Azure CLI with the `azure-devops` extension.")
		}
		return providerCLI{}, errors.New("Teamwork Graph CLI (`twg`) could not be found at the Context path or on PATH. Set its executable path in Settings → Contexts → Providers, or install TWG CLI.")
	}
	return providerCLI{provider, resolved, context}, nil
}

func (c providerCLI) run(args ...string) (json.RawMessage, error) {
	command := append([]string(nil), args...)
	if c.provider == "atlassian" {
		if (args[0] == "jira" || args[0] == "confluence") && (c.context.AtlassianSite == nil || strings.TrimSpace(*c.context.AtlassianSite) == "") {
			return nil, errors.New("Atlassian site is not configured for this Context")
		}
		if args[0] == "bitbucket" && (c.context.BitbucketWorkspace == nil || strings.TrimSpace(*c.context.BitbucketWorkspace) == "") {
			return nil, errors.New("Bitbucket workspace is not configured for this Context")
		}
		if c.context.AtlassianSite != nil && strings.TrimSpace(*c.context.AtlassianSite) != "" {
			command = append(command, "--site", strings.TrimSpace(*c.context.AtlassianSite))
		}
		if c.context.BitbucketWorkspace != nil && strings.TrimSpace(*c.context.BitbucketWorkspace) != "" {
			command = append(command, "--workspace", strings.TrimSpace(*c.context.BitbucketWorkspace))
		}
		command = append(command, "--output", "json")
	} else {
		if c.context.AzureDevOpsOrganization == nil || strings.TrimSpace(*c.context.AzureDevOpsOrganization) == "" {
			return nil, errors.New("an Azure DevOps organization is not configured for this Context")
		}
		org := strings.TrimSpace(*c.context.AzureDevOpsOrganization)
		if !strings.HasPrefix(org, "https://") {
			org = "https://dev.azure.com/" + strings.Trim(org, "/")
		}
		command = append(command, "--organization", strings.TrimRight(org, "/"), "-o", "json")
	}
	out, err := exec.Command(c.executable, command...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			message := strings.TrimSpace(string(exit.Stderr))
			if message == "" {
				message = "the command returned a non-zero exit status"
			}
			if c.provider == "azure_dev_ops" && azureDevOpsExtensionMissing(message) {
				message = "Azure DevOps CLI extension `azure-devops` is unavailable. install it with `az extension add --name azure-devops`. " + message
			}
			return nil, fmt.Errorf("%s CLI failed: %s", c.providerLabel(), message)
		}
		return nil, fmt.Errorf("could not run %s CLI: %w", c.providerLabel(), err)
	}
	var value json.RawMessage
	if err := json.Unmarshal(out, &value); err != nil {
		return nil, fmt.Errorf("%s CLI returned invalid JSON: %w", c.providerLabel(), err)
	}
	return value, nil
}
func (c providerCLI) providerLabel() string {
	if c.provider == "atlassian" {
		return "Atlassian TWG"
	}
	return "Azure DevOps"
}
func azureDevOpsExtensionMissing(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "extension") || strings.Contains(message, "command not found") || strings.Contains(message, "not a valid command") || strings.Contains(message, "not recognized")
}

func fetchAtlassianSnapshot(ctx domain.Context, object classifiedExternalObject, fetchedAt int64) (externalSnapshot, error) {
	if err := validateProviderContextTarget(ctx, object); err != nil {
		return externalSnapshot{}, err
	}
	cli, err := newProviderCLI("atlassian", ctx.TWGExecutablePath, ctx)
	if err != nil {
		return externalSnapshot{}, err
	}
	var args []string
	switch object.Kind {
	case domain.ObjectIssue:
		key, err := providerKey(object.Key, "jira:")
		if err != nil {
			return externalSnapshot{}, err
		}
		args = []string{"jira", "workitem", "get", key}
	case domain.ObjectDocument:
		id, err := providerKey(object.Key, "confluence:")
		if err != nil {
			return externalSnapshot{}, err
		}
		args = []string{"confluence", "content", "get", id, "--detail", "full"}
	case domain.ObjectPullRequest:
		args = []string{"bitbucket", "pull-requests", "get", object.URL}
	default:
		return externalSnapshot{}, errors.New("generic links do not have an Atlassian snapshot")
	}
	raw, err := cli.run(args...)
	if err != nil {
		return externalSnapshot{}, err
	}
	data := unwrapProviderData(raw)
	title := firstProviderString(data, "title", "summary", "name")
	if title == "" {
		return externalSnapshot{}, errors.New("TWG returned an object without a title")
	}
	state := firstProviderString(data, "state", "status")
	if object.Kind == domain.ObjectDocument {
		state = providerVersion(data)
	}
	metadata := []externalMetadata{}
	if object.Kind == domain.ObjectDocument {
		if state != "" {
			metadata = append(metadata, externalMetadata{"version", state})
		}
	} else {
		for _, key := range []string{"key", "issueKey", "number", "id", "author", "assignee", "priority", "type", "created", "updated", "url"} {
			if value := providerFieldString(data, key); value != "" {
				metadata = append(metadata, externalMetadata{key, value})
			}
		}
	}
	return externalSnapshot{Title: title, State: state, Metadata: metadata, FetchedAt: fetchedAt}, nil
}
func fetchAtlassianDocument(ctx domain.Context, object classifiedExternalObject) (string, error) {
	if err := validateProviderContextTarget(ctx, object); err != nil {
		return "", err
	}
	cli, err := newProviderCLI("atlassian", ctx.TWGExecutablePath, ctx)
	if err != nil {
		return "", err
	}
	var args []string
	field := "description"
	switch object.Kind {
	case domain.ObjectIssue:
		key, e := providerKey(object.Key, "jira:")
		if e != nil {
			return "", e
		}
		args = []string{"jira", "workitem", "get", key}
		field = "description"
	case domain.ObjectDocument:
		id, e := providerKey(object.Key, "confluence:")
		if e != nil {
			return "", e
		}
		args = []string{"confluence", "content", "get", id, "--detail", "full"}
		field = "body"
	default:
		return "", errors.New("this Atlassian object does not have a readable document")
	}
	raw, err := cli.run(args...)
	if err != nil {
		return "", err
	}
	return providerRender(providerField(unwrapProviderData(raw), field)), nil
}
func fetchAtlassianComments(ctx domain.Context, object classifiedExternalObject) ([]externalComment, error) {
	if err := validateProviderContextTarget(ctx, object); err != nil {
		return nil, err
	}
	cli, err := newProviderCLI("atlassian", ctx.TWGExecutablePath, ctx)
	if err != nil {
		return nil, err
	}
	var args []string
	switch object.Kind {
	case domain.ObjectIssue:
		key, e := providerKey(object.Key, "jira:")
		if e != nil {
			return nil, e
		}
		args = []string{"jira", "workitem", "comment", "query", key}
	case domain.ObjectDocument:
		id, e := providerKey(object.Key, "confluence:")
		if e != nil {
			return nil, e
		}
		args = []string{"confluence", "content", "comments", "list", id}
	case domain.ObjectPullRequest:
		args = []string{"bitbucket", "pull-requests", "comment", "query", object.URL}
	default:
		return nil, errors.New("comments are not available for this Atlassian object")
	}
	raw, err := cli.run(args...)
	if err != nil {
		return nil, err
	}
	data := unwrapProviderData(raw)
	entries := providerArray(data)
	if entries == nil {
		entries = providerArray(providerField(data, "comments"))
	}
	comments := make([]externalComment, 0, len(entries))
	for i, value := range entries {
		id, _ := strconv.ParseInt(providerFieldString(value, "id"), 10, 64)
		if id < 0 {
			id = int64(i)
		}
		author := providerFieldString(value, "author")
		if author == "" {
			author = providerFieldString(value, "displayName")
		}
		if author == "" {
			author = "Unknown"
		}
		body := providerField(value, "body")
		if body == nil {
			body = providerField(value, "text")
		}
		created := providerField(value, "created")
		if created == nil {
			created = providerField(value, "createdAt")
		}
		if created == nil {
			created = providerField(value, "created_at")
		}
		comments = append(comments, externalComment{id, author, providerRender(body), providerRender(created)})
	}
	return comments, nil
}

func fetchAzureSnapshot(ctx domain.Context, object classifiedExternalObject, fetchedAt int64) (externalSnapshot, error) {
	if err := validateProviderContextTarget(ctx, object); err != nil {
		return externalSnapshot{}, err
	}
	cli, err := newProviderCLI("azure_dev_ops", ctx.AZExecutablePath, ctx)
	if err != nil {
		return externalSnapshot{}, err
	}
	orgProjectID, ok := strings.CutPrefix(object.Key, "ado:")
	if !ok {
		return externalSnapshot{}, errors.New("invalid Azure DevOps object identifier")
	}
	orgProject, id, ok := strings.Cut(orgProjectID, "#")
	if !ok {
		return externalSnapshot{}, errors.New("invalid Azure DevOps object identifier")
	}
	organization, project, ok := strings.Cut(orgProject, "/")
	if !ok || organization == "" || project == "" {
		return externalSnapshot{}, errors.New("invalid Azure DevOps object identifier")
	}
	args := []string{"boards", "work-item", "show", "--id", id, "--fields", "System.Id,System.Title,System.State,System.WorkItemType,System.AssignedTo,System.CreatedDate,System.ChangedDate,System.Tags,System.Description"}
	if object.Kind == domain.ObjectPullRequest {
		args = []string{"repos", "pr", "show", "--id", id}
	} else if object.Kind != domain.ObjectIssue {
		return externalSnapshot{}, errors.New("only Azure DevOps work items and pull requests have snapshots")
	}
	raw, err := cli.run(args...)
	if err != nil {
		return externalSnapshot{}, err
	}
	data := raw
	if object.Kind == domain.ObjectIssue {
		if fields := providerField(data, "fields"); fields != nil {
			data = fields
		}
	}
	title := firstProviderString(data, "System.Title", "title")
	if title == "" {
		title = firstProviderString(raw, "title", "name")
	}
	if title == "" {
		return externalSnapshot{}, errors.New("Azure DevOps returned an object without a title")
	}
	state := firstProviderString(data, "System.State", "state")
	if object.Kind == domain.ObjectPullRequest {
		state = firstProviderString(raw, "status", "state")
	}
	metadata := []externalMetadata{{"id", id}}
	for _, pair := range [][2]string{{"type", "System.WorkItemType"}, {"assignee", "System.AssignedTo"}, {"created", "System.CreatedDate"}, {"updated", "System.ChangedDate"}, {"tags", "System.Tags"}} {
		if v := providerFieldString(data, pair[1]); v != "" {
			metadata = append(metadata, externalMetadata{pair[0], v})
		}
	}
	metadata = append(metadata, externalMetadata{"project", project}, externalMetadata{"organization", organization})
	return externalSnapshot{Title: title, State: state, Metadata: metadata, FetchedAt: fetchedAt}, nil
}
func fetchAzureDocument(ctx domain.Context, object classifiedExternalObject) (string, error) {
	if err := validateProviderContextTarget(ctx, object); err != nil {
		return "", err
	}
	if object.Kind != domain.ObjectIssue {
		return "", errors.New("only Azure DevOps work items have a readable description")
	}
	cli, err := newProviderCLI("azure_dev_ops", ctx.AZExecutablePath, ctx)
	if err != nil {
		return "", err
	}
	id := strings.TrimPrefix(object.Key, "ado:")
	_, id, ok := strings.Cut(id, "#")
	if !ok {
		return "", errors.New("invalid Azure DevOps object identifier")
	}
	raw, err := cli.run("boards", "work-item", "show", "--id", id, "--fields", "System.Description")
	if err != nil {
		return "", err
	}
	data := providerField(raw, "fields")
	if data == nil {
		data = raw
	}
	return providerFieldString(data, "System.Description"), nil
}
func fetchAzureComments(ctx domain.Context, object classifiedExternalObject) ([]externalComment, error) {
	if err := validateProviderContextTarget(ctx, object); err != nil {
		return nil, err
	}
	cli, err := newProviderCLI("azure_dev_ops", ctx.AZExecutablePath, ctx)
	if err != nil {
		return nil, err
	}
	key := strings.TrimPrefix(object.Key, "ado:")
	orgProject, id, ok := strings.Cut(key, "#")
	if !ok {
		return nil, errors.New("invalid Azure DevOps object identifier")
	}
	_, project, ok := strings.Cut(orgProject, "/")
	if !ok {
		return nil, errors.New("invalid Azure DevOps object identifier")
	}
	if object.Kind == domain.ObjectIssue {
		raw, e := cli.run("devops", "invoke", "--area", "wit", "--resource", "workItems/{workItemId}/comments", "--route-parameters", "project="+project, "workItemId="+id, "--api-version", "7.1-preview.4", "--http-method", "GET")
		if e != nil {
			return nil, e
		}
		data := providerField(raw, "comments")
		entries := providerArray(data)
		if entries == nil {
			entries = providerArray(raw)
		}
		out := []externalComment{}
		for i, v := range entries {
			out = append(out, externalComment{int64(i), providerFieldString(v, "createdBy"), providerFieldString(v, "text"), providerFieldString(v, "createdDate")})
		}
		return out, nil
	}
	if object.Kind == domain.ObjectPullRequest {
		raw, e := cli.run("repos", "pr", "show", "--id", id)
		if e != nil {
			return nil, e
		}
		repo := providerFieldString(providerField(raw, "repository"), "id")
		if repo == "" {
			return nil, errors.New("Azure DevOps did not return a repository id for this pull request")
		}
		threads, e := cli.run("devops", "invoke", "--area", "git", "--resource", "repositories/{repositoryId}/pullRequests/{pullRequestId}/threads", "--route-parameters", "project="+project, "repositoryId="+repo, "pullRequestId="+id, "--api-version", "7.1", "--http-method", "GET")
		if e != nil {
			return nil, e
		}
		values := providerArray(providerField(threads, "value"))
		if values == nil {
			values = providerArray(threads)
		}
		out := []externalComment{}
		for _, thread := range values {
			if providerFieldBool(thread, "isDeleted") {
				continue
			}
			for _, v := range providerArray(providerField(thread, "comments")) {
				if providerFieldBool(v, "isDeleted") || strings.EqualFold(providerFieldString(v, "commentType"), "system") || strings.EqualFold(providerFieldString(v, "commentType"), "codeChange") {
					continue
				}
				out = append(out, externalComment{0, providerFieldString(v, "author"), providerFieldString(v, "content"), providerFieldString(v, "publishedDate")})
			}
		}
		return out, nil
	}
	return nil, errors.New("comments are available only for Azure DevOps work items and pull requests")
}

func providerKey(key, prefix string) (string, error) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return "", fmt.Errorf("invalid %s identifier", strings.TrimSuffix(prefix, ":"))
	}
	_, value, ok := strings.Cut(rest, "#")
	if !ok || value == "" {
		return "", errors.New("invalid provider object identifier")
	}
	return value, nil
}
func unwrapProviderData(raw json.RawMessage) json.RawMessage {
	value := providerField(raw, "data")
	if value != nil {
		return value
	}
	return raw
}
func providerField(raw json.RawMessage, key string) json.RawMessage {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	if object, ok := value.(map[string]any); ok {
		if exact, exists := object[key]; exists {
			encoded, _ := json.Marshal(exact)
			if string(encoded) == "null" {
				return nil
			}
			return encoded
		}
	}
	for _, part := range strings.Split(key, ".") {
		m, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = m[part]
	}
	encoded, _ := json.Marshal(value)
	if string(encoded) == "null" {
		return nil
	}
	return encoded
}
func providerFieldString(raw json.RawMessage, key string) string {
	value := providerField(raw, key)
	if len(value) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(value, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(value, &n) == nil {
		return n.String()
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) == nil {
		for _, k := range []string{"displayName", "name", "value", "login"} {
			if v := providerField(value, k); len(v) > 0 {
				var x string
				if json.Unmarshal(v, &x) == nil {
					return x
				}
			}
		}
	}
	return ""
}
func firstProviderString(raw json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := providerFieldString(raw, key); value != "" {
			return value
		}
	}
	return ""
}
func providerArray(raw json.RawMessage) []json.RawMessage {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) == nil {
		return values
	}
	return nil
}
func providerRender(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	if value := providerFieldString(raw, "storage.value"); value != "" {
		return value
	}
	if value := providerFieldString(raw, "value"); value != "" {
		return value
	}
	if value := providerFieldString(raw, "text"); value != "" {
		return value
	}
	var node any
	if json.Unmarshal(raw, &node) == nil {
		var collect func(any, *strings.Builder)
		collect = func(value any, out *strings.Builder) {
			switch item := value.(type) {
			case map[string]any:
				if text, ok := item["text"].(string); ok {
					out.WriteString(text)
				}
				if content, ok := item["content"].([]any); ok {
					for _, child := range content {
						collect(child, out)
					}
					if kind, _ := item["type"].(string); kind == "paragraph" || kind == "heading" || kind == "listItem" {
						out.WriteByte('\n')
					}
				}
			case []any:
				for _, child := range item {
					collect(child, out)
				}
			}
		}
		var text strings.Builder
		collect(node, &text)
		if rendered := strings.TrimSpace(text.String()); rendered != "" {
			return rendered
		}
	}
	return string(raw)
}
func providerVersion(raw json.RawMessage) string {
	if v := providerFieldString(raw, "version.number"); v != "" {
		return v
	}
	return providerFieldString(raw, "version")
}
func providerFieldBool(raw json.RawMessage, key string) bool {
	var value bool
	_ = json.Unmarshal(providerField(raw, key), &value)
	return value
}

func classifyLocalMarkdown(repositoryID int64, root, rawPath string) (classifiedExternalObject, error) {
	rootPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		return classifiedExternalObject{}, err
	}
	rootPath, err = filepath.Abs(rootPath)
	if err != nil {
		return classifiedExternalObject{}, err
	}
	pathValue := rawPath
	if strings.HasPrefix(pathValue, "file://") {
		parsed, e := url.Parse(pathValue)
		if e != nil {
			return classifiedExternalObject{}, e
		}
		pathValue, e = url.PathUnescape(parsed.Path)
		if e != nil {
			return classifiedExternalObject{}, e
		}
	}
	pathValue, err = filepath.Abs(pathValue)
	if err != nil {
		return classifiedExternalObject{}, err
	}
	path, err := filepath.EvalSymlinks(pathValue)
	if err != nil {
		return classifiedExternalObject{}, err
	}
	rel, err := filepath.Rel(rootPath, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return classifiedExternalObject{}, errors.New("local Markdown file is outside the registered Repository checkout")
	}
	ext := strings.ToLower(filepath.Ext(rel))
	if ext != ".md" && ext != ".markdown" {
		return classifiedExternalObject{}, errors.New("only Markdown files can be linked as local work")
	}
	relativeName := filepath.ToSlash(rel)
	isSpec := strings.EqualFold(filepath.Base(relativeName), "spec.md")
	isTicket := strings.EqualFold(filepath.Base(filepath.Dir(relativeName)), "issues")
	if !isSpec && !isTicket {
		return classifiedExternalObject{}, errors.New("local work must be a spec.md file or a Markdown file in an issues directory")
	}
	if info, e := os.Stat(path); e != nil || !info.Mode().IsRegular() {
		return classifiedExternalObject{}, errors.New("local Markdown path is not a file")
	}
	rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	return classifiedExternalObject{Provider: domain.ProviderGeneric, Kind: domain.ObjectGeneric, Key: fmt.Sprintf("local:%d#%s", repositoryID, rel), URL: fileURL}, nil
}

func readLocalMarkdownSnapshot(path string, fetchedAt int64) (externalSnapshot, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return externalSnapshot{}, fmt.Errorf("Local Markdown file %q could not be read: %w", path, err)
	}
	title := markdownTitle(string(body), path)
	state := markdownStatus(string(body))
	return externalSnapshot{Title: title, State: state, Metadata: []externalMetadata{{"status", state}}, FetchedAt: fetchedAt}, nil
}

func readLocalMarkdownComments(path string) ([]externalComment, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Local Markdown file %q is missing or unreadable: %w", path, err)
	}
	_, comments := markdownBodyAndComments(string(body))
	if comments == "" {
		return []externalComment{}, nil
	}
	return []externalComment{{ID: 0, Author: "Local Markdown", Body: comments}}, nil
}
func readLocalMarkdownDocument(path, key string) (IssueDocument, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return IssueDocument{}, fmt.Errorf("Local Markdown file %q is missing or unreadable: %w", path, err)
	}
	markdown := string(body)
	main, _ := markdownBodyAndComments(markdown)
	doc := IssueDocument{Body: main, BodyFormat: "markdown", SubIssues: []subIssue{}}
	if filepath.Base(path) != "spec.md" {
		return doc, nil
	}
	directory := filepath.Join(filepath.Dir(path), "issues")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return IssueDocument{}, fmt.Errorf("Local Markdown ticket directory %q could not be read: %w", directory, err)
	}
	repo, relative, ok := strings.Cut(strings.TrimPrefix(key, "local:"), "#")
	if !ok {
		return IssueDocument{}, errors.New("The local Markdown Spec has an invalid repository path")
	}
	sortEntries(entries)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".md" && ext != ".markdown" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		raw, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		number := 0
		if prefix, _, ok := strings.Cut(entry.Name(), "-"); ok {
			number, _ = strconv.Atoi(prefix)
		}
		ticketRel := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(relative)), "issues", entry.Name()))
		doc.SubIssues = append(doc.SubIssues, subIssue{Number: number, Title: markdownTitle(string(raw), path), State: markdownStatus(string(raw)), URL: fmt.Sprintf("local:%s#%s", repo, ticketRel)})
	}
	return doc, nil
}
func sortEntries(entries []os.DirEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Name() < entries[j-1].Name(); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}
func markdownTitle(markdown, path string) string {
	for _, line := range strings.Split(markdown, "\n") {
		if title, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok && strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if name != "" {
		return name
	}
	return "Local Markdown"
}
func markdownStatus(markdown string) string {
	lines := strings.Split(markdown, "\n")
	if len(lines) > 12 {
		lines = lines[:12]
	}
	for _, line := range lines {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "status") && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "Open"
}
func markdownBodyAndComments(markdown string) (string, string) {
	body := []string{}
	comments := []string{}
	inComments := false
	for _, line := range strings.Split(markdown, "\n") {
		if inComments && strings.HasPrefix(strings.TrimSpace(line), "## ") {
			break
		}
		if strings.EqualFold(strings.TrimSpace(line), "## Comments") {
			inComments = true
			continue
		}
		if inComments {
			comments = append(comments, line)
		} else {
			body = append(body, line)
		}
	}
	return strings.TrimSpace(strings.Join(body, "\n")), strings.TrimSpace(strings.Join(comments, "\n"))
}
