// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// A private repository must route every job, including reusable-workflow
// jobs, to the org runner. Hosted jobs stop before execution at the budget.
func TestVerificationRunsWithoutHostedMinutes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	for _, name := range []string{"ci.yml"} {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../.github/workflows", name))
			if err != nil {
				t.Fatal(err)
			}
			var workflow struct {
				Jobs map[string]struct {
					RunsOn string            `yaml:"runs-on"`
					Uses   string            `yaml:"uses"`
					With   map[string]string `yaml:"with"`
					Steps  []struct {
						Uses string         `yaml:"uses"`
						With map[string]any `yaml:"with"`
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
					if job.With["runs_on"] != "linux-vm" {
						t.Errorf("%s does not route reusable jobs to linux-vm", id)
					}
					var runners []string
					if err := json.Unmarshal([]byte(job.With["test_os"]), &runners); err != nil || len(runners) != 1 || runners[0] != "linux-vm" {
						t.Errorf("%s leaves the test matrix on hosted runners", id)
					}
					continue
				}
				if job.RunsOn != "linux-vm" {
					t.Errorf("%s runs on %q, want linux-vm", id, job.RunsOn)
				}
				node := false
				for _, step := range job.Steps {
					if strings.HasPrefix(step.Uses, "actions/setup-node@") {
						node = true
					}
					if strings.HasPrefix(step.Uses, "oven-sh/setup-bun@") && !node {
						t.Errorf("%s needs real Node before Bun on the bare runner", id)
					}
					if strings.HasPrefix(step.Uses, "actions/setup-go@") && step.With["cache"] != false {
						t.Errorf("%s must not restore a hosted Go cache over the persistent cache", id)
					}
				}
			}
		})
	}
}
