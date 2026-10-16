// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package managed_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestModuleDependsOnNothing asserts the promise the README makes: importing
// this port adds no dependency to anyone's build. A contract that drags a
// transport, a logger, or a vendor SDK behind it is not a contract a third
// party can adopt cheaply, and the cost is invisible until someone runs
// `go mod graph`.
func TestModuleDependsOnNothing(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "latere.ai/x/managed-agents") {
			continue
		}
		// A standard-library path has no dot in its first element.
		first, _, _ := strings.Cut(line, "/")
		if strings.Contains(first, ".") {
			t.Errorf("non-standard-library dependency: %s", line)
		}
	}
}

func TestModuleGraphContainsOnlyThePort(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "all").CombinedOutput()
	if err != nil {
		t.Fatalf("read consumer module graph: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "latere.ai/x/managed-agents" {
		t.Fatalf("the port adds tool dependencies to its consumers:\n%s", got)
	}
}
