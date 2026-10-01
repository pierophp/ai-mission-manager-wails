package pstack

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTreeHashMatchesTauriBuildScript(t *testing.T) {
	if TreeHash != "e703a1d0f440c3a796e40973f2c284220b1aa45604ddd75fb9bb199540dde018" {
		t.Fatalf("Go tree hash = %s, want the build.rs hash", TreeHash)
	}
	hash := sha256.New()
	for _, file := range Files {
		contents, err := assets.ReadFile("assets/pstack/" + file.Path)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write([]byte(file.Path))
		hash.Write([]byte{0})
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(contents)))
		hash.Write(size[:])
		hash.Write(contents)
		if file.Executable {
			hash.Write([]byte{1})
		} else {
			hash.Write([]byte{0})
		}
	}
	if got := fmt.Sprintf("%x", hash.Sum(nil)); got != TreeHash {
		t.Fatalf("manifest hash = %s, recomputed = %s", TreeHash, got)
	}
}

func TestEmbeddedPstackInstallsAtomicallyWithExecutableModes(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".local/share/ai-mission-manager/pstack", TreeHash)
	if err := InstallLocal(target); err != nil {
		t.Fatal(err)
	}
	for _, file := range Files {
		info, err := os.Stat(filepath.Join(target, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm()&0o111 != 0; got != file.Executable {
			t.Errorf("%s executable = %v, want %v", file.Path, got, file.Executable)
		}
	}
	marker := filepath.Join(target, "preserve-on-noop")
	if err := os.WriteFile(marker, []byte("existing tree"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallLocal(target); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "existing tree" {
		t.Fatalf("existing tree was replaced: %q, %v", got, err)
	}
}

func TestRemoteInstallerStreamsEmbeddedFilesThroughStdin(t *testing.T) {
	target := filepath.Join(t.TempDir(), "remote", TreeHash)
	payload, err := Payload()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", RemoteInstallCommand(target))
	command.Stdin = strings.NewReader(string(payload))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("remote install: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(target, "skills/poteto-mode/SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestVendoredSkillsAndReferencesRemainValid(t *testing.T) {
	for _, skill := range []string{"grilling", "to-spec", "to-tickets", "implement"} {
		if contents, err := Skill(skill); err != nil || len(contents) == 0 {
			t.Fatalf("embedded skill %q missing: %v", skill, err)
		}
	}
	if failures := checkVendor(); len(failures) > 0 {
		t.Fatal(strings.Join(failures, "\n"))
	}
}

var markdownLink = regexp.MustCompile(`\]\(([^)]+)\)`)
var codeSpan = regexp.MustCompile("`([^`\\n]+)`")
var pathToken = regexp.MustCompile(`^[^<>\s]+/[^<>\s]+\.(md|mjs|ts|tsx|js|sh|json|ya?ml)$`)

func checkVendor() []string {
	var failures []string
	err := fs.WalkDir(assets, "assets/pstack", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !strings.Contains(" .md .mjs .ts .tsx .js .sh .json .yaml .yml ", " "+strings.TrimPrefix(ext, ".")+" ") {
			return nil
		}
		contents, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		text := string(contents)
		if filepath.Base(name) != "PATCHES.md" {
			for _, rule := range []struct {
				name string
				re   *regexp.Regexp
			}{{"removed goal command", regexp.MustCompile(`(?i)/goal\b`)}, {"removed Cursor team kit", regexp.MustCompile(`(?i)cursor-team-kit`)}, {"removed cloud execution field", regexp.MustCompile(`(?i)environment:\s*[\"']cloud[\"']`)}, {"removed Cursor home path", regexp.MustCompile(`(?i)~/.cursor/`)}} {
				if rule.re.MatchString(text) {
					failures = append(failures, fmt.Sprintf("%s contains %s", name, rule.name))
				}
			}
		}
		return nil
	})
	if err != nil {
		return []string{err.Error()}
	}
	modeRoot := "assets/pstack/skills/poteto-mode"
	err = fs.WalkDir(assets, modeRoot, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(name) != ".md" {
			return nil
		}
		contents, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		source := string(contents)
		var references []string
		for _, match := range markdownLink.FindAllStringSubmatch(source, -1) {
			if value := cleanPath(match[1]); value != "" {
				references = append(references, filepath.ToSlash(filepath.Join(filepath.Dir(name), value)), filepath.ToSlash(filepath.Join("assets/pstack", value)), filepath.ToSlash(filepath.Join(modeRoot, value)))
			}
		}
		for _, match := range codeSpan.FindAllStringSubmatch(source, -1) {
			for _, value := range strings.Fields(match[1]) {
				if pathToken.MatchString(value) {
					references = append(references, filepath.ToSlash(filepath.Join(filepath.Dir(name), value)), filepath.ToSlash(filepath.Join("assets/pstack", value)), filepath.ToSlash(filepath.Join(modeRoot, value)))
				}
			}
		}
		for index := 0; index < len(references); index += 3 {
			found := false
			for _, candidate := range references[index : index+3] {
				if _, err := assets.ReadFile(candidate); err == nil {
					found = true
					break
				}
			}
			if !found {
				failures = append(failures, name+": referenced path is missing: "+references[index])
			}
		}
		return nil
	})
	if err != nil {
		failures = append(failures, err.Error())
	}
	return failures
}

func cleanPath(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "<>"))
	if value == "" || value == "url" || strings.HasPrefix(value, "#") || regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`).MatchString(value) {
		return ""
	}
	if before, _, found := strings.Cut(value, "?"); found {
		value = before
	}
	if before, _, found := strings.Cut(value, "#"); found {
		value = before
	}
	return value
}
