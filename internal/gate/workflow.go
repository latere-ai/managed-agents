// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package ci holds the checks on how this repository's CI is wired. The
// module is separate so the tool requirement and the YAML parser stay out of
// the port's module graph; its tests read .github/workflows. The helpers live
// in a non-test file so the module has statements the cover floor can measure.
package ci

import "strings"

// hosted reports whether a runner label is one of GitHub's hosted images.
func hosted(label string) bool {
	return strings.HasPrefix(label, "ubuntu-") || strings.HasPrefix(label, "macos-") || strings.HasPrefix(label, "windows-")
}

// shared reports whether a uses reference is one of the organization's shared
// pipelines in latere-ai/ci at its major tag. That tag is how a fix to the
// shared pipeline reaches every repository, and the daily ci-gate bump
// dispatches only a caller at it.
func shared(uses string) bool {
	path, ref, ok := strings.Cut(uses, "@")
	return ok && ref == "v1" && strings.HasPrefix(path, "latere-ai/ci/.github/workflows/")
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
