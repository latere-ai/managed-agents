// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	managed "latere.ai/x/managed-agents"
)

// graderName is the subagent the worker spawns to grade a round.
const graderName = "grader"

// graderTools are the only tools the grader holds: it reads the work and
// cannot change it. A grader holding any other tool fails the round.
var graderTools = []string{"read", "grep", "glob"}

// graderAttempts is how many times one round may be graded before a grader
// that never answers with a verdict ends the session failed, the same limit
// the local runtime keeps for its judge.
const graderAttempts = 2

// graderInstructions is the grader's standing role. The rubric travels in
// each round's task; what to do with it does not change.
const graderInstructions = `You grade a reproduction against the rubric below. The work is in the working directory. You may read it and you must not change it.

Read the files. Judge each criterion independently, on the evidence present in the files, and cite where you found it.

End your turn with exactly one JSON object, after any reasoning, in this shape:

{
  "grade": "passed" | "failed" | "needs_revision",
  "explanation": "why, in a few sentences",
  "criteria": [
    {"id": 1, "name": "short name", "passed": true, "evidence": "where you saw it"}
  ]
}

Every rubric criterion gets a row, in order, whether it passed or not. "passed" means every criterion passed. "needs_revision" means the work is close and a further attempt would fix it. "failed" means it would not.`

// graderRole is the grader's instructions for one session: its standing role
// and the session's rubric. The rubric is part of the agent's version, so a
// later round reads the same rubric without the adapter keeping a copy.
func graderRole(rubric string) string {
	return graderInstructions + "\n\n--- Rubric ---\n\n" + strings.TrimSpace(rubric)
}

// roundTask is the task the grader subagent is started with. The adapter
// compares the started subagent's task with it exactly.
func roundTask(round int) string {
	return fmt.Sprintf("Grade round %d of this session's work against your rubric.", round)
}

// spawnRequest is the message that asks the worker to start the grader. The
// worker only relays the task; the adapter reads the verdict from the
// grader's own conversation.
func spawnRequest(task string) string {
	return "Your work is being graded now. Call the spawn tool once, with agent \"" + graderName +
		"\" and exactly the task between the lines below, nothing added or changed and no tools argument. " +
		"Do not grade the work yourself and do not change any file. When the grader answers, end your turn.\n\n" +
		"-----\n" + task + "\n-----"
}

// revisionLead opens a revision's message, before the grader's reasoning. It
// is also how a later poll recognizes the message as a revision.
const revisionLead = "--- The grader rejected the previous attempt. Its reasoning follows; address it, then end your turn. ---\n\n"

// judgeVerdict is what the grader's final message must hold.
type judgeVerdict struct {
	Grade       string           `json:"grade"`
	Explanation string           `json:"explanation"`
	Criteria    []judgeCriterion `json:"criteria"`
}

type judgeCriterion struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

var grades = map[string]managed.Grade{
	"passed":         managed.GradePassed,
	"failed":         managed.GradeFailed,
	"needs_revision": managed.GradeNeedsRevision,
}

// parseVerdict reads the last JSON object in the grader's final message that
// has a grade, and turns it into the contract's verdict. The failed criteria
// are appended to the explanation, and the model that graded is named, as
// the local runtime's judge verdict does.
func parseVerdict(message string, model managed.ModelRef) (managed.Verdict, error) {
	found, err := lastVerdictObject(message)
	if err != nil {
		return managed.Verdict{}, err
	}
	grade, ok := grades[strings.ToLower(strings.TrimSpace(found.Grade))]
	if !ok {
		return managed.Verdict{}, fmt.Errorf("the grader returned grade %q, which is not one of passed, failed, needs_revision", found.Grade)
	}
	if len(found.Criteria) == 0 {
		return managed.Verdict{}, errors.New("the grader returned no per-criterion detail")
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(found.Explanation))
	first := true
	for _, criterion := range found.Criteria {
		if criterion.Passed {
			continue
		}
		if first {
			b.WriteString("\n\nFailed criteria:")
			first = false
		}
		fmt.Fprintf(&b, "\n  %d. %s: %s", criterion.ID, criterion.Name, strings.TrimSpace(criterion.Evidence))
	}
	if model.Valid() {
		fmt.Fprintf(&b, "\n\nGraded by %s/%s.", model.Provider, model.ID)
	}
	return managed.Verdict{Grade: grade, Explanation: strings.TrimSpace(b.String())}, nil
}

// lastVerdictObject finds the last JSON object in text that decodes as a
// verdict with a grade. A model may wrap it in a code fence or put reasoning
// before it; anything after it is ignored.
func lastVerdictObject(text string) (judgeVerdict, error) {
	for i := strings.LastIndexByte(text, '{'); i >= 0; i = strings.LastIndexByte(text[:i], '{') {
		decoder := json.NewDecoder(bytes.NewReader([]byte(text[i:])))
		var candidate judgeVerdict
		if decoder.Decode(&candidate) == nil && candidate.Grade != "" {
			return candidate, nil
		}
	}
	return judgeVerdict{}, errors.New("the grader's last message holds no verdict object")
}

// graderStarted reports whether a started subagent is the grader the adapter
// asked for: the declared subagent, exactly the round's task, and no tool
// beyond the read-only ones.
func graderStarted(started wireSubagentStarted, task string) error {
	if started.Agent.Name != graderName {
		return fmt.Errorf("the worker started %q instead of the grader", started.Agent.Name)
	}
	if strings.TrimSpace(started.Task) != strings.TrimSpace(task) {
		return errors.New("the worker started the grader with a task other than the round's")
	}
	for _, tool := range started.Tools {
		if !slices.Contains(graderTools, tool) {
			return fmt.Errorf("the grader held the tool %q, which can change the work", tool)
		}
	}
	return nil
}
