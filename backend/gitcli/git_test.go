package gitcli

import (
	"errors"
	"reflect"
	"strings"
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

func TestWorktreeBranchPreparationUsesFetchAndAllAddVariants(t *testing.T) {
	base := "git -C '/repo'"
	branch := "mission-MC-1"
	setup := func(local, remote string) *fakeShell {
		return &fakeShell{results: map[string]string{
			base + " remote": "upstream\n", base + " remote get-url 'upstream'": "https://example.invalid/app.git\n",
			"if [ -e '/tmp/worktree' ]; then printf exists; else printf missing; fi":                            "missing",
			base + " show-ref --verify --quiet 'refs/remotes/upstream/main'; printf 'status:%s' \"$?\"":         "status:0",
			base + " worktree list --porcelain":                                                                 "worktree /repo\nHEAD abc\nbranch refs/heads/main\n",
			base + " show-ref --verify --quiet 'refs/heads/mission-MC-1'; printf 'status:%s' \"$?\"":            local,
			base + " show-ref --verify --quiet 'refs/remotes/upstream/mission-MC-1'; printf 'status:%s' \"$?\"": remote,
		}, err: map[string]error{}}
	}
	t.Run("new branch from fetched remote base", func(t *testing.T) {
		shell := setup("status:1", "status:1")
		if err := New(shell).PrepareWorktree(domain.Machine{}, "/repo", "/tmp/worktree", branch, "main", "https://example.invalid/app.git", false); err != nil {
			t.Fatal(err)
		}
		want := []string{"if [ -e '/tmp/worktree' ]; then printf exists; else printf missing; fi", base + " remote", base + " remote get-url 'upstream'", base + " fetch 'upstream'", base + " show-ref --verify --quiet 'refs/remotes/upstream/main'; printf 'status:%s' \"$?\"", base + " worktree list --porcelain", base + " show-ref --verify --quiet 'refs/heads/mission-MC-1'; printf 'status:%s' \"$?\"", base + " show-ref --verify --quiet 'refs/remotes/upstream/mission-MC-1'; printf 'status:%s' \"$?\"", "mkdir -p '/tmp'", base + " worktree add -b 'mission-MC-1' '/tmp/worktree' 'refs/remotes/upstream/main'", "git -C '/tmp/worktree' config 'branch.mission-MC-1.remote' 'upstream'", "git -C '/tmp/worktree' config 'branch.mission-MC-1.merge' 'refs/heads/mission-MC-1'"}
		if !reflect.DeepEqual(shell.calls, want) {
			t.Fatalf("Git commands = %#v, want %#v", shell.calls, want)
		}
	})
	t.Run("reuse local branch", func(t *testing.T) {
		shell := setup("status:0", "status:1")
		newLocalBranch, err := New(shell).PrepareWorktreeWithResult(domain.Machine{}, "/repo", "/tmp/worktree", branch, "main", "https://example.invalid/app.git", true)
		if err != nil || newLocalBranch {
			t.Fatalf("reused local branch result = %t, %v", newLocalBranch, err)
		}
		if got := shell.calls[9]; got != base+" worktree add '/tmp/worktree' 'mission-MC-1'" {
			t.Fatalf("add command = %q", got)
		}
	})
	t.Run("reuse remote branch", func(t *testing.T) {
		shell := setup("status:1", "status:0")
		newLocalBranch, err := New(shell).PrepareWorktreeWithResult(domain.Machine{}, "/repo", "/tmp/worktree", branch, "main", "https://example.invalid/app.git", true)
		if err != nil || !newLocalBranch {
			t.Fatalf("remote branch attach result = %t, %v", newLocalBranch, err)
		}
		if got := shell.calls[9]; got != base+" worktree add --track -b 'mission-MC-1' '/tmp/worktree' 'upstream/mission-MC-1'" {
			t.Fatalf("add command = %q", got)
		}
		if err := New(shell).RollbackPreparedWorktree(domain.Machine{}, "/repo", "/tmp/worktree", branch, newLocalBranch); err != nil {
			t.Fatal(err)
		}
		wantCleanup := []string{base + " worktree remove '/tmp/worktree'", base + " branch -D 'mission-MC-1'"}
		if got := shell.calls[len(shell.calls)-2:]; !reflect.DeepEqual(got, wantCleanup) {
			t.Fatalf("remote branch rollback commands = %#v, want %#v", got, wantCleanup)
		}
	})
	t.Run("reuse missing branch fails", func(t *testing.T) {
		shell := setup("status:1", "status:1")
		if err := New(shell).PrepareWorktree(domain.Machine{}, "/repo", "/tmp/worktree", branch, "main", "https://example.invalid/app.git", true); err == nil || !strings.Contains(err.Error(), "does not exist locally or on remote") {
			t.Fatalf("missing reuse branch error = %v", err)
		}
	})
	t.Run("configured remote base branch is required", func(t *testing.T) {
		shell := setup("status:1", "status:1")
		shell.results[base+" show-ref --verify --quiet 'refs/remotes/upstream/main'; printf 'status:%s' \"$?\""] = "status:1"
		if err := New(shell).PrepareWorktree(domain.Machine{}, "/repo", "/tmp/worktree", branch, "main", "https://example.invalid/app.git", false); err == nil || !strings.Contains(err.Error(), "base branch main does not exist") {
			t.Fatalf("missing base branch error = %v", err)
		}
	})
}

func TestWorktreePreparationCleansNewCheckoutWhenUpstreamConfigurationFails(t *testing.T) {
	base := "git -C '/repo'"
	mergeConfig := "git -C '/tmp/worktree' config 'branch.mission-MC-1.merge' 'refs/heads/mission-MC-1'"
	shell := &fakeShell{
		results: map[string]string{
			"if [ -e '/tmp/worktree' ]; then printf exists; else printf missing; fi": "missing",
			base + " remote":                  "origin\n",
			base + " remote get-url 'origin'": "https://example.invalid/app.git\n",
			base + " show-ref --verify --quiet 'refs/remotes/origin/main'; printf 'status:%s' \"$?\"": "status:0",
			base + " worktree list --porcelain": "worktree /repo\nHEAD abc\nbranch refs/heads/main\n",
			base + " show-ref --verify --quiet 'refs/heads/mission-MC-1'; printf 'status:%s' \"$?\"":          "status:1",
			base + " show-ref --verify --quiet 'refs/remotes/origin/mission-MC-1'; printf 'status:%s' \"$?\"": "status:1",
		},
		err: map[string]error{mergeConfig: errors.New("config write failed")},
	}
	err := New(shell).PrepareWorktree(domain.Machine{}, "/repo", "/tmp/worktree", "mission-MC-1", "main", "https://example.invalid/app.git", false)
	if err == nil || !strings.Contains(err.Error(), "config write failed") {
		t.Fatalf("upstream configuration failure = %v", err)
	}
	wantCleanup := []string{base + " worktree remove '/tmp/worktree'", base + " branch -D 'mission-MC-1'"}
	if got := shell.calls[len(shell.calls)-2:]; !reflect.DeepEqual(got, wantCleanup) {
		t.Fatalf("cleanup commands = %#v, want %#v", got, wantCleanup)
	}
}

func TestListWorktreesParsesPorcelainAndRemoveAddsForceOnlyWhenRequested(t *testing.T) {
	base := "git -C '/repo'"
	shell := &fakeShell{results: map[string]string{base + " worktree list --porcelain": "worktree /repo\nHEAD abc\nbranch refs/heads/main\n\nworktree /tmp/wt\nHEAD def\nbranch refs/heads/mission-MC-1\n\nworktree /tmp/detached\nHEAD ghi\ndetached\n"}, err: map[string]error{}}
	got, err := New(shell).ListWorktrees(domain.Machine{}, "/repo")
	if err != nil || len(got) != 3 || got[1].Path != "/tmp/wt" || got[1].Branch != "mission-MC-1" || !got[2].Detached {
		t.Fatalf("ListWorktrees() = %#v, %v", got, err)
	}
	if err := New(shell).RemoveWorktree(domain.Machine{}, "/repo", "/tmp/wt", false); err != nil {
		t.Fatal(err)
	}
	if err := New(shell).RemoveWorktree(domain.Machine{}, "/repo", "/tmp/dirty", true); err != nil {
		t.Fatal(err)
	}
	if got, want := shell.calls[1:], []string{base + " worktree remove '/tmp/wt'", base + " worktree remove --force '/tmp/dirty'"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("remove commands = %#v, want %#v", got, want)
	}
}
