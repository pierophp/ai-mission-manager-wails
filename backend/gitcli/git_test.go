package gitcli

import (
	"errors"
	"reflect"
	"testing"

	"github.com/piero/ai-mission-manager-wails/backend/domain"
)

type fakeShell struct {
	results map[string]string
	err     map[string]error
	calls   []string
}

func (f *fakeShell) RunShell(_ domain.Machine, command string) (string, error) {
	f.calls = append(f.calls, command)
	return f.results[command], f.err[command]
}

func TestInspectCheckoutUsesGitCLIAndReturnsRepositoryMetadata(t *testing.T) {
	base := "git -C '/src/it'\\''s-app'"
	shell := &fakeShell{results: map[string]string{
		base + " rev-parse --show-toplevel":                " /src/it's-app\n",
		base + " symbolic-ref --short HEAD":                "main\n",
		base + " status --porcelain --untracked-files=all": " M file\n",
		base + " remote":                  "origin\n",
		base + " remote get-url 'origin'": "git@example.com:app.git\n",
	}, err: map[string]error{}}
	got, err := New(shell).Inspect(domain.Machine{}, "/src/it's-app")
	if err != nil {
		t.Fatal(err)
	}
	want := Checkout{RemoteURL: "git@example.com:app.git", Branch: "main", Dirty: true}
	if got != want {
		t.Fatalf("Inspect() = %#v, want %#v", got, want)
	}
	wantCalls := []string{base + " rev-parse --show-toplevel", base + " symbolic-ref --short HEAD", base + " status --porcelain --untracked-files=all", base + " remote", base + " remote get-url 'origin'"}
	if !reflect.DeepEqual(shell.calls, wantCalls) {
		t.Fatalf("Git commands = %#v, want %#v", shell.calls, wantCalls)
	}
}

func TestInspectCheckoutAllowsDetachedHEAD(t *testing.T) {
	base := "git -C '/checkout'"
	shell := &fakeShell{results: map[string]string{
		base + " rev-parse --show-toplevel":                "/checkout\n",
		base + " status --porcelain --untracked-files=all": "",
		base + " remote":                    "upstream\n",
		base + " remote get-url 'upstream'": "https://example.com/repo.git\n",
	}, err: map[string]error{base + " symbolic-ref --short HEAD": errors.New("detached")}}
	got, err := New(shell).Inspect(domain.Machine{}, "/checkout")
	if err != nil || got.Branch != "HEAD (detached)" {
		t.Fatalf("Inspect() = %#v, %v", got, err)
	}
}

func TestAdoptCheckoutChecksConfiguredRemote(t *testing.T) {
	base := "git -C '/checkout'"
	shell := &fakeShell{results: map[string]string{
		base + " rev-parse --show-toplevel":                "/checkout\n",
		base + " symbolic-ref --short HEAD":                "main\n",
		base + " status --porcelain --untracked-files=all": "",
		base + " remote":                  "origin\n",
		base + " remote get-url 'origin'": "https://example.com/repo.git\n",
	}, err: map[string]error{}}
	if _, err := New(shell).Adopt(domain.Machine{}, "/checkout", "https://example.com/repo.git"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(shell).Adopt(domain.Machine{}, "/checkout", "https://other.example/repo.git"); err == nil || err.Error() != "Repository remote does not match the configured remote: expected https://other.example/repo.git, found https://example.com/repo.git" {
		t.Fatalf("Adopt mismatch error = %v", err)
	}
}

func TestCloneQuotesArgumentsAndRejectsBlankRemote(t *testing.T) {
	shell := &fakeShell{results: map[string]string{}, err: map[string]error{}}
	if err := New(shell).Clone(domain.Machine{}, "  git@example.com:team/repo.git ", "/tmp/a path"); err != nil {
		t.Fatal(err)
	}
	if got, want := shell.calls[0], "git clone 'git@example.com:team/repo.git' '/tmp/a path'"; got != want {
		t.Fatalf("clone command = %q, want %q", got, want)
	}
	if err := New(shell).Clone(domain.Machine{}, "  ", "/tmp/repo"); err == nil {
		t.Fatal("Clone accepted a blank remote")
	}
}
