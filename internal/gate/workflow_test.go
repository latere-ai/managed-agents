// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// A public repository runs every job on GitHub-hosted runners: the
// organization's own runner group refuses a public repository, and a fork's
// pull request must never run on that shared machine. Every action and
// reusable workflow is pinned by commit, so a moved tag cannot change what CI
// runs.
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
					RunsOn string            `yaml:"runs-on"`
					Uses   string            `yaml:"uses"`
					With   map[string]string `yaml:"with"`
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
					if !hosted(job.With["runs_on"]) {
						t.Errorf("%s routes reusable jobs to %q, want a GitHub-hosted runner", id, job.With["runs_on"])
					}
					// An empty test_os tests on runs_on, checked above.
					var runners []string
					if raw := job.With["test_os"]; raw != "" {
						if err := json.Unmarshal([]byte(raw), &runners); err != nil {
							t.Errorf("%s test_os %q does not parse: %v", id, raw, err)
						}
					}
					for _, runner := range runners {
						if !hosted(runner) {
							t.Errorf("%s tests on %q, want a GitHub-hosted runner", id, runner)
						}
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

// hosted reports whether a runner label is one of GitHub's hosted images.
func hosted(label string) bool {
	return strings.HasPrefix(label, "ubuntu-") || strings.HasPrefix(label, "macos-") || strings.HasPrefix(label, "windows-")
}

// pinned reports whether a uses reference names a full commit.
func pinned(uses string) bool {
	_, ref, ok := strings.Cut(uses, "@")
	if !ok || len(ref) != 40 {
		return false
	}
	for _, c := range ref {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
