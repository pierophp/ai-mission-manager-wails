package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

func TestGHCLIParsesSnapshotCommentsAndIssueDocument(t *testing.T) {
	cli := fakeGH(t, `case "$*" in
*"--json body") printf '%s' '{"body":"Description"}' ;;
"issue view "*) printf '%s' '{"number":7,"title":"Spec","state":"OPEN","author":{"login":"piero"},"labels":[{"name":"backend"}],"milestone":null,"createdAt":"2025-01-01","updatedAt":"2025-01-02"}' ;;
		"api repos/acme/app/issues/7/comments"*) printf '%s' '[[{"id":4,"user":{"login":"dev"},"body":"Hello","created_at":"2025-01-02"},{"id":5,"user":null,"body":"No author","created_at":"2025-01-03"}]]' ;;
"api repos/acme/app/issues/7/sub_issues"*) printf '%s\n' '{"number":8,"title":"Child","state":"OPEN","html_url":"https://github.com/Acme/App/issues/8"}' ;;
*) echo "unexpected gh args: $*" >&2; exit 2;;
esac`)
	o, _ := classifyExternalURL("https://github.com/acme/app/issues/7")
	snapshot, err := cli.fetchSnapshot(o, 123)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Title != "Spec" || snapshot.FetchedAt != 123 || len(snapshot.Metadata) != 5 {
		t.Fatalf("snapshot %#v", snapshot)
	}
	comments, err := cli.fetchComments(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 || comments[0].Author != "dev" || comments[1].Author != "Unknown" {
		t.Fatalf("comments %#v", comments)
	}
	doc, err := cli.fetchIssueDocument(o)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Body != "Description" || doc.BodyFormat != "markdown" || len(doc.SubIssues) != 1 || doc.SubIssues[0].URL != "https://github.com/acme/app/issues/8" {
		t.Fatalf("document %#v", doc)
	}
}

func TestGHCLICreateAndCommentArguments(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	cli := fakeGH(t, `printf '%s\n' "$*" >> "$GH_TEST_LOG"
	case "$*" in
	"api repos/acme/app/issues "*) printf '%s' '{"html_url":"https://github.com/acme/app/issues/10"}' ;;
	"issue comment https://github.com/acme/app/issues/10 "*) exit 0 ;;
*) echo "unexpected gh args: $*" >&2; exit 2;;
esac`)
	t.Setenv("GH_TEST_LOG", log)
	url, err := cli.createIssue("acme/app", "New issue", "Body")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/acme/app/issues/10" {
		t.Fatalf("url %s", url)
	}
	if err = cli.addComment(url, "  Looks good \n"); err != nil {
		t.Fatal(err)
	}
	if err = cli.addComment(url, " "); err == nil {
		t.Fatal("blank comment accepted")
	}
	data, _ := os.ReadFile(log)
	for _, want := range []string{"api repos/acme/app/issues --method POST --raw-field title=New issue --raw-field body=Body", "issue comment https://github.com/acme/app/issues/10 --body   Looks good "} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("args log missing %q: %s", want, data)
		}
	}
}

func fakeGH(t *testing.T, body string) ghCLI {
	t.Helper()
	file := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return ghCLI{file}
}

func TestAtlassianProviderParsesSnapshotDocumentCommentsAndArguments(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "twg-args")
	cli := fakeCommand(t, "twg", argsPath, `case "$1 $2 $3" in
"jira workitem get") printf '%s' '{"key":"APP-4","summary":"Jira item","status":"Open","description":"Body"}' ;;
"jira workitem comment") printf '%s' '{"comments":[{"id":9,"author":{"displayName":"Ada"},"body":"Comment","created":"2026-09-01"}]}' ;;
"confluence content get") printf '%s' '{"id":"6","title":"Runbook","version":{"number":3},"body":{"storage":{"value":"<p>Docs</p>"}}}' ;;
"confluence content comments") printf '%s' '{"comments":[]}' ;;
"bitbucket pull-requests get") printf '%s' '{"id":7,"title":"PR","state":"OPEN"}' ;;
"bitbucket pull-requests comment") printf '%s' '{"comments":[]}' ;;
esac`)
	ctx := domain.Context{TWGExecutablePath: stringPtr(cli), AtlassianSite: stringPtr("https://acme.atlassian.net"), BitbucketWorkspace: stringPtr("team")}
	jira, _ := classifyExternalURL("https://acme.atlassian.net/browse/APP-4")
	snapshot, err := fetchAtlassianSnapshot(ctx, jira, 11)
	if err != nil || snapshot.Title != "Jira item" || snapshot.State != "Open" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	doc, err := fetchAtlassianDocument(ctx, jira)
	if err != nil || doc != "Body" {
		t.Fatalf("document=%q err=%v", doc, err)
	}
	comments, err := fetchAtlassianComments(ctx, jira)
	if err != nil || len(comments) != 1 || comments[0].Author != "Ada" {
		t.Fatalf("comments=%#v err=%v", comments, err)
	}
	page, _ := classifyExternalURL("https://acme.atlassian.net/wiki/spaces/ENG/pages/6/Runbook")
	pageSnapshot, err := fetchAtlassianSnapshot(ctx, page, 12)
	if err != nil || pageSnapshot.Title != "Runbook" || pageSnapshot.State != "3" {
		t.Fatalf("Confluence snapshot=%#v err=%v", pageSnapshot, err)
	}
	pageBody, err := fetchAtlassianDocument(ctx, page)
	if err != nil || pageBody != "<p>Docs</p>" {
		t.Fatalf("Confluence document=%q err=%v", pageBody, err)
	}
	pr, _ := classifyExternalURL("https://bitbucket.org/team/repo/pull-requests/7")
	prSnapshot, err := fetchAtlassianSnapshot(ctx, pr, 13)
	if err != nil || prSnapshot.Title != "PR" {
		t.Fatalf("Bitbucket snapshot=%#v err=%v", prSnapshot, err)
	}
	calls, _ := os.ReadFile(argsPath)
	for _, want := range []string{"jira\nworkitem\nget\nAPP-4\n--site\nhttps://acme.atlassian.net\n--workspace\nteam\n--output\njson", "jira\nworkitem\ncomment\nquery\nAPP-4\n--site\nhttps://acme.atlassian.net\n--workspace\nteam\n--output\njson", "confluence\ncontent\nget\n6\n--detail\nfull\n--site\nhttps://acme.atlassian.net\n--workspace\nteam\n--output\njson", "bitbucket\npull-requests\nget\nhttps://bitbucket.org/team/repo/pull-requests/7\n--site\nhttps://acme.atlassian.net\n--workspace\nteam\n--output\njson"} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("missing command %q in %s", want, calls)
		}
	}
}

func TestAzureDevOpsProviderUsesOrganizationAndHintsMissingExtension(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "az-args")
	cli := fakeCommand(t, "az", argsPath, `case "$1 $2 $3" in
"boards work-item show") printf '%s' '{"id":42,"fields":{"System.Title":"Azure item","System.State":"Active","System.WorkItemType":"Bug","System.Description":"<p>Body</p>"}}' ;;
"repos pr show") printf '%s' '{"pullRequestId":43,"title":"Azure PR","status":"active","repository":{"id":"repo-1"}}' ;;
esac
case "$*" in
*"resource workItems/"*) printf '%s' '{"comments":[{"commentId":5,"createdBy":{"displayName":"Grace"},"text":"Azure comment","createdDate":"2026-09-02"}]}' ;;
*"resource repositories/"*) printf '%s' '{"value":[{"id":1,"comments":[{"id":6,"author":{"displayName":"Lin"},"content":"Azure comment","commentType":"text","publishedDate":"2026-09-02"}]}]}' ;;
esac`)
	ctx := domain.Context{AZExecutablePath: stringPtr(cli), AzureDevOpsOrganization: stringPtr("acme")}
	object, _ := classifyExternalURL("https://dev.azure.com/acme/apps/_workitems/edit/42")
	snapshot, err := fetchAzureSnapshot(ctx, object, 12)
	if err != nil || snapshot.Title != "Azure item" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	doc, err := fetchAzureDocument(ctx, object)
	if err != nil || doc != "<p>Body</p>" {
		t.Fatalf("document=%q err=%v", doc, err)
	}
	calls, _ := os.ReadFile(argsPath)
	if !strings.Contains(string(calls), "--organization\nhttps://dev.azure.com/acme\n-o\njson") {
		t.Fatalf("organization/json args missing: %s", calls)
	}
	workItemComments, err := fetchAzureComments(ctx, object)
	if err != nil || len(workItemComments) != 1 || workItemComments[0].Author != "Grace" {
		t.Fatalf("Azure work item comments=%#v err=%v", workItemComments, err)
	}
	pr, _ := classifyExternalURL("https://dev.azure.com/acme/apps/_git/repo/pullrequest/43")
	prSnapshot, err := fetchAzureSnapshot(ctx, pr, 14)
	if err != nil || prSnapshot.Title != "Azure PR" {
		t.Fatalf("Azure PR snapshot=%#v err=%v", prSnapshot, err)
	}
	prComments, err := fetchAzureComments(ctx, pr)
	if err != nil || len(prComments) != 1 || prComments[0].Body != "Azure comment" {
		t.Fatalf("Azure PR comments=%#v err=%v", prComments, err)
	}

	missing := fakeCommand(t, "az-missing", filepath.Join(t.TempDir(), "missing-args"), `echo 'ERROR: command not recognized' >&2; exit 2`)
	ctx.AZExecutablePath = stringPtr(missing)
	_, err = fetchAzureSnapshot(ctx, object, 13)
	if err == nil || !strings.Contains(err.Error(), "az extension add --name azure-devops") {
		t.Fatalf("missing extension hint = %v", err)
	}
}

func TestContextProviderTargetMismatchSurfacesWarningWithoutRejectingLink(t *testing.T) {
	ctx := domain.Context{Name: "Personal", AtlassianSite: stringPtr("acme"), BitbucketWorkspace: stringPtr("acme-code"), AzureDevOpsOrganization: stringPtr("https://dev.azure.com/acme-engineering")}
	for _, raw := range []string{"https://other.atlassian.net/browse/APP-1", "https://bitbucket.org/another-team/app/pull-requests/4", "https://dev.azure.com/another-org/apps/_workitems/edit/17"} {
		if warning := contextProviderMismatchWarning(ctx, raw); !strings.Contains(warning, "The Link was created") {
			t.Fatalf("mismatch warning for %s = %q", raw, warning)
		}
	}
	if warning := contextProviderMismatchWarning(ctx, "https://acme.atlassian.net/browse/APP-1"); warning != "" {
		t.Fatalf("matching target warning = %q", warning)
	}
}

func TestLocalMarkdownClassificationAndSpecDocument(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "feature", "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(root, "feature", "spec.md")
	ticket := filepath.Join(root, "feature", "issues", "01-ticket.md")
	if err := os.WriteFile(spec, []byte("# Feature\nStatus: Active\nBody\n## Comments\nReview note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ticket, []byte("# Ticket\nStatus: Done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	object, err := classifyLocalMarkdown(3, root, spec)
	if err != nil || object.Key != "local:3#feature/spec.md" {
		t.Fatalf("object=%#v err=%v", object, err)
	}
	snapshot, err := readLocalMarkdownSnapshot(spec, 23)
	if err != nil || snapshot.Title != "Feature" || snapshot.State != "Active" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	doc, err := readLocalMarkdownDocument(spec, object.Key)
	if err != nil || len(doc.SubIssues) != 1 || doc.SubIssues[0].Title != "Ticket" || doc.SubIssues[0].State != "Done" {
		t.Fatalf("doc=%#v err=%v", doc, err)
	}
	comments, err := readLocalMarkdownComments(spec)
	if err != nil || len(comments) != 1 || comments[0].Body != "Review note" {
		t.Fatalf("comments=%#v err=%v", comments, err)
	}
	ticketObject, err := classifyLocalMarkdown(3, root, ticket)
	if err != nil || ticketObject.Key != "local:3#feature/issues/01-ticket.md" {
		t.Fatalf("ticket classification=%#v err=%v", ticketObject, err)
	}
	readme := filepath.Join(root, "README.md")
	if err = os.WriteFile(readme, []byte("# README"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = classifyLocalMarkdown(3, root, readme); err == nil {
		t.Fatal("unrelated Markdown should not classify as a Spec or Ticket")
	}
	if _, err := classifyLocalMarkdown(3, root, filepath.Join(t.TempDir(), "outside.md")); err == nil {
		t.Fatal("outside file should be rejected")
	}
}

func fakeCommand(t *testing.T, name, log, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \""+log+"\"\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
