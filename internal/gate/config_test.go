// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"os/exec"
	"testing"
)

func TestGateLoadsRepositoryConfig(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "tool", "lateregate", "list", "-C", "../..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the tool invoked by make cannot read the repository config: %v\n%s", err, out)
	}
}
