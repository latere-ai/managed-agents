// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
)

// A public repository runs every job on GitHub-hosted runners: the
// organization's own runner group refuses a public repository, and a fork's
// pull request must never run on that shared machine. The shared workflow
// routes a public repository to a hosted runner by default, so the caller
// passes no runner. Every action and reusable workflow is pinned by commit, so
// a moved tag cannot change what CI runs.
func TestVerificationRunsOnHostedRunners(t *testing.T) {
	// Relative to this package's directory, where go test runs it. A path from
	// runtime.Caller is module-relative when the test is built with -trimpath.
	for _, name := range []string{"ci.yml"} {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("../../.github/workflows", name))
			if err != nil {
				t.Fatal(err)
			}
			var workflow struct {
				Jobs map[string]struct {
					RunsOn string `yaml:"runs-on"`
					Uses   string `yaml:"uses"`
					Steps  []struct {
						Uses string `yaml:"uses"`
					} `yaml:"steps"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal(body, &workflow); err != nil {
				t.Fatal(err)
			}
			if len(workflow.Jobs) == 0 {
				t.Fatal("workflow has no jobs")
			}
			for id, job := range workflow.Jobs {
				if job.Uses != "" {
					if !pinned(job.Uses) {
						t.Errorf("%s uses %q, not pinned by commit", id, job.Uses)
					}
					continue
				}
				if !hosted(job.RunsOn) {
					t.Errorf("%s runs on %q, want a GitHub-hosted runner", id, job.RunsOn)
				}
				for _, step := range job.Steps {
					if step.Uses != "" && !pinned(step.Uses) {
						t.Errorf("%s uses %q, not pinned by commit", id, step.Uses)
					}
				}
			}
		})
	}
}

func TestHostedNamesGitHubImages(t *testing.T) {
	for label, want := range map[string]bool{
		"ubuntu-latest":  true,
		"macos-latest":   true,
		"windows-latest": true,
		"linux-vm":       false,
		"self-hosted":    false,
		"":               false,
	} {
		if got := hosted(label); got != want {
			t.Errorf("hosted(%q) = %v, want %v", label, got, want)
		}
	}
}

func TestPinnedWantsAFullCommit(t *testing.T) {
	const commit = "a75a02092efa2533724d45e2e0161bb9392816b7"
	for uses, want := range map[string]bool{
		"owner/repo/.github/workflows/w.yml@" + commit:            true,
		"owner/repo/.github/workflows/w.yml@v1":                   false,
		"owner/repo/.github/workflows/w.yml":                      false,
		"owner/repo/.github/workflows/w.yml@" + commit[:39]:       false,
		"owner/repo/.github/workflows/w.yml@" + commit[:39] + "G": false,
		"owner/repo/.github/workflows/w.yml@" + commit[:39] + "A": false,
	} {
		if got := pinned(uses); got != want {
			t.Errorf("pinned(%q) = %v, want %v", uses, got, want)
		}
	}
}
