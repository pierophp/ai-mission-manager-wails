package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
