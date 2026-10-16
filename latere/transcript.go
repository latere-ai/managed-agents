// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"encoding/json"
	"strings"

	managed "latere.ai/x/managed-agents"
)

// CostMeter is the extra meter a Latere session's usage carries its cost in:
// the micro-USD the platform reported for each model call and each paid tool
// call. No rate card prices it. The person's own Latere wallet is what the
// platform charged, and a job paid by the person records it as reported.
const CostMeter = managed.MeterChargedCost

// freeVariant is the billing variant of a model the platform serves at no
// charge, named free/<vendor>/<model>.
const freeVariant = "free"

// modelRef reads a platform model name, <vendor>/<model> or
// free/<vendor>/<model>, as a provider-qualified reference. A name with no
// vendor (an Effort level, tier/<level>) is kept whole under provider
// "latere", the platform that names it.
func modelRef(name string) managed.ModelRef {
	name = strings.TrimSpace(name)
	variant := ""
	if rest, ok := strings.CutPrefix(name, freeVariant+"/"); ok {
		name, variant = rest, freeVariant
	}
	vendor, id, ok := strings.Cut(name, "/")
	if !ok || vendor == "tier" {
		return managed.ModelRef{Provider: platformProvider, ID: name, BillingVariant: variant}
	}
	return managed.ModelRef{Provider: vendor, ID: id, BillingVariant: variant}
}

// platformProvider is the provider of a model the platform names but no
// vendor serves under that name: an Effort level.
const platformProvider = "latere"

// modelName is the inverse of modelRef: the name the platform takes.
func modelName(ref managed.ModelRef) string {
	name := ref.ID
	if ref.Provider != platformProvider {
		name = ref.Provider + "/" + ref.ID
	}
	if ref.BillingVariant == freeVariant {
		name = freeVariant + "/" + name
	}
	return name
}

// callUsage is one model call's usage as an event delta. Lux reports input
// without the cached part, so fresh input is input_tokens as given; reasoning
// tokens are already part of output and are kept as a detail, never billed a
// second time.
func callUsage(request wireModelRequest) *managed.Usage {
	if request.Usage == nil && request.Cost == nil {
		return nil
	}
	usage := &managed.Usage{Model: modelRef(request.Model)}
	if u := request.Usage; u != nil {
		usage.InputFresh = u.InputTokens
		usage.Output = u.OutputTokens
		if u.CacheRead != nil {
			usage.InputCached = *u.CacheRead
		}
		if u.CacheWrite != nil {
			usage.CacheWrite = *u.CacheWrite
		}
		if u.ReasoningTokens > 0 {
			usage.Details = map[string]int64{"reasoning": u.ReasoningTokens}
		}
	}
	if request.Cost != nil && *request.Cost > 0 {
		usage.Extra = map[managed.Meter]int64{CostMeter: *request.Cost}
	}
	return usage
}

// toolCost is a paid tool call's cost (web search) as an event delta.
func toolCost(result wireToolResult) *managed.Usage {
	if result.Cost == nil || *result.Cost <= 0 {
		return nil
	}
	return &managed.Usage{Extra: map[managed.Meter]int64{CostMeter: *result.Cost}}
}

// normalize maps the platform's events onto the contract's transcript. Every
// event keeps its platform id, so a re-read never changes an event's
// identity, and its payload as raw evidence. Events of the grader's thread
// are kept and marked by their thread, so a reader can tell the worker's
// account from the grader's.
func normalize(events []wireEvent) []managed.Event {
	out := make([]managed.Event, 0, len(events))
	for _, event := range events {
		normalized := managed.Event{ID: event.ID, Time: event.Time, Kind: managed.EventKind(event.Type), Role: managed.RoleSystem, Raw: event.Payload}
		switch event.Type {
		case "user.message":
			var message wireUserMessage
			_ = json.Unmarshal(event.Payload, &message)
			normalized.Role, normalized.Kind, normalized.Content = managed.RoleUser, managed.KindMessage, text(message.Content)
		case "agent.message":
			var message wireAgentMessage
			_ = json.Unmarshal(event.Payload, &message)
			normalized.Role, normalized.Kind = managed.RoleAgent, managed.KindMessage
			normalized.Content = text(message.Message.Blocks)
			if normalized.Content == "" && thinking(message.Message.Blocks) != "" {
				normalized.Kind, normalized.Content = managed.KindThinking, thinking(message.Message.Blocks)
			}
		case "agent.tool_use":
			var use wireToolUse
			_ = json.Unmarshal(event.Payload, &use)
			normalized.Role, normalized.Kind, normalized.Tool, normalized.Content = managed.RoleAgent, managed.KindToolUse, use.Name, string(use.Input)
		case "tool.result":
			var result wireToolResult
			_ = json.Unmarshal(event.Payload, &result)
			normalized.Role, normalized.Kind, normalized.Content = managed.RoleSystem, managed.KindToolResult, text(result.Content)
			normalized.Usage = toolCost(result)
		case "model.request":
			var request wireModelRequest
			_ = json.Unmarshal(event.Payload, &request)
			normalized.Usage = callUsage(request)
		case "session.error":
			var failure wireSessionError
			_ = json.Unmarshal(event.Payload, &failure)
			normalized.Content = strings.TrimSpace(failure.Code + ": " + failure.Message)
		}
		if event.Thread != "" {
			normalized.Tool = strings.TrimSpace(normalized.Tool)
			if normalized.Content != "" {
				normalized.Content = "[" + graderName + "] " + normalized.Content
			}
		}
		out = append(out, normalized)
	}
	return out
}

func text(blocks []wireBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func thinking(blocks []wireBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "thinking" && strings.TrimSpace(block.Thinking) != "" {
			parts = append(parts, block.Thinking)
		}
	}
	return strings.Join(parts, "\n\n")
}

// total sums every event's usage into the session's cumulative usage. The
// sum of non-negative deltas over a growing log cannot decrease, which is
// what the contract asks of cumulative usage.
func total(events []managed.Event, model managed.ModelRef) managed.Usage {
	sum := managed.Usage{Model: model}
	for _, event := range events {
		if event.Usage == nil {
			continue
		}
		delta := *event.Usage
		delta.Model = model
		sum.Add(delta)
	}
	return sum
}
