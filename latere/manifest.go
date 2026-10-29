// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// The worker's manifest, as the platform's Agent manifest of
// topos.latere.ai/v1 spells it, kept to the fields this adapter sets.

type agentManifest struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   agentMetadata `json:"metadata"`
	Spec       agentSpec     `json:"spec"`
}

type agentMetadata struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

type agentSpec struct {
	Model          agentModel          `json:"model"`
	Instructions   string              `json:"instructions"`
	Tools          []string            `json:"tools"`
	Subagents      []subagent          `json:"subagents,omitempty"`
	Permissions    []map[string]string `json:"permissions,omitempty"`
	Approvals      approvals           `json:"approvals"`
	SubagentLimits subagentLimits      `json:"subagentLimits"`
	Machine        machine             `json:"machine"`
	Limits         limits              `json:"limits"`
	Context        struct{}            `json:"context"`
}

type agentModel struct {
	Name string `json:"name"`
}

type subagent struct {
	Name string    `json:"name"`
	Spec agentSpec `json:"spec"`
}

type approvals struct {
	Mode        string   `json:"mode"`
	AlwaysAllow []string `json:"alwaysAllow,omitempty"`
}

type subagentLimits struct {
	MaxDepth      *int `json:"maxDepth,omitempty"`
	MaxConcurrent *int `json:"maxConcurrent,omitempty"`
}

type machine struct {
	Kind  string `json:"kind"`
	Image string `json:"image,omitempty"`
}

type limits struct {
	TurnTimeout string `json:"turnTimeout"`
	MaxAge      string `json:"maxAge"`
}

// workerTools are the built-in tools a reproduction needs. Every one is
// always allowed, because a session nobody attends would otherwise stop on a
// confirmation and wait forever.
var workerTools = []string{"read", "write", "edit", "bash", "grep", "glob", "web_fetch", "todo"}

// cellaActions are what the agent's identity needs for its first tool call to
// open a machine, each on every resource. The manifest spells each as an
// action and resource pair.
var cellaActions = []string{
	"cella:sandbox.create", "cella:sandbox.read", "cella:sandbox.update", "cella:sandbox.exec",
	"cella:sandbox.delete", "cella:secret.create", "cella:secret.read", "cella:secret.update",
	"cella:secret.mount", "cella:environment.use",
}

func cellaPermissions() []map[string]string {
	out := make([]map[string]string, len(cellaActions))
	for i, action := range cellaActions {
		out[i] = map[string]string{"action": action, "resource": "*"}
	}
	return out
}

// The session's limits. A reproduction's one turn can run for hours, past
// the platform's two-hour default; the session itself is bounded at two
// days, long enough for every revision round.
const (
	turnTimeout = "6h"
	maxAge      = "48h"
)

// workerManifest is the agent one session runs as. model is the name the
// session starts on (an Effort level or a specific model), and graderModel,
// when the session is graded, the model the grader runs on (a subagent must
// name a model, not a level) with the rubric it grades against.
func workerManifest(product Product, instructions, image, model, graderModel, rubric string) (agentManifest, error) {
	one := 1
	spec := agentSpec{
		Model:        agentModel{Name: model},
		Instructions: instructions,
		Tools:        append([]string(nil), workerTools...),
		Permissions:  cellaPermissions(),
		Approvals:    approvals{Mode: "progressive", AlwaysAllow: append([]string(nil), workerTools...)},
		Machine:      machine{Kind: "cella", Image: image},
		Limits:       limits{TurnTimeout: turnTimeout, MaxAge: maxAge},
	}
	if graderModel != "" {
		spec.SubagentLimits = subagentLimits{MaxDepth: &one, MaxConcurrent: &one}
		spec.Subagents = []subagent{{Name: graderName, Spec: agentSpec{
			Model:        agentModel{Name: graderModel},
			Instructions: graderRole(rubric),
			Tools:        append([]string(nil), graderTools...),
			Approvals:    approvals{Mode: "progressive"},
			Machine:      machine{Kind: "cella"},
			Limits:       limits{TurnTimeout: turnTimeout, MaxAge: maxAge},
		}}}
	}
	name, err := agentName(product, spec)
	if err != nil {
		return agentManifest{}, err
	}
	return agentManifest{
		APIVersion: "topos.latere.ai/v1", Kind: "Agent", Spec: spec,
		Metadata: agentMetadata{Name: name, DisplayName: product.Name},
	}, nil
}

// agentName names an agent by its spec's digest, so applying the same spec
// again reuses one agent, a changed method or model gets its own, and a
// session pinned to an agent's version never sees it change under it.
func agentName(product Product, spec agentSpec) (string, error) {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("encode the agent's spec: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return product.Key + "-" + hex.EncodeToString(sum[:])[:16], nil
}
