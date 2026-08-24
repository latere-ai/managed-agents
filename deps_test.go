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
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
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
