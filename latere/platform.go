// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The platform's wire, kept to the fields this adapter reads. Topos's own Go
// types are not imported: Topos is pre-1.0 and changes them freely, and the
// adapter depends on the HTTP contract, which the platform documents and
// versions, rather than on another module's internals.

type wireSession struct {
	ID         string        `json:"id"`
	Status     string        `json:"status"`
	StopReason string        `json:"stop_reason"`
	LastSeq    uint64        `json:"last_seq"`
	Model      *wireModelRef `json:"model,omitempty"`
	Budget     struct {
		Spent int64 `json:"spent_cost_usd_micro"`
	} `json:"budget"`
	Network   wireNetwork       `json:"network"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Initiator struct {
		Subject string `json:"subject"`
	} `json:"initiator"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type wireModelRef struct {
	Name string `json:"name"`
	Via  string `json:"via,omitempty"`
}

type wireNetwork struct {
	Mode   string `json:"mode"`
	Source string `json:"source,omitempty"`
}

type wireEvent struct {
	ID       string          `json:"id"`
	Seq      uint64          `json:"seq"`
	Type     string          `json:"type"`
	Time     time.Time       `json:"time"`
	Turn     int             `json:"turn,omitempty"`
	Subagent string          `json:"subagent,omitempty"`
	Payload  json.RawMessage `json:"payload"`
}

type wireBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

type wireAgentMessage struct {
	Message struct {
		Blocks []wireBlock `json:"blocks"`
	} `json:"message"`
	StopReason string `json:"stop_reason"`
}

type wireUserMessage struct {
	Content []wireBlock `json:"content"`
}

type wireToolUse struct {
	ToolUseID string          `json:"tool_use_id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type wireToolResult struct {
	ToolUseID string      `json:"tool_use_id"`
	Content   []wireBlock `json:"content"`
	IsError   bool        `json:"is_error,omitempty"`
	Cost      *int64      `json:"cost_usd_micro,omitempty"`
}

type wireModelRequest struct {
	Model   string     `json:"model"`
	Usage   *wireUsage `json:"usage,omitempty"`
	Cost    *int64     `json:"cost_usd_micro,omitempty"`
	Outcome string     `json:"outcome"`
}

// wireUsage is Lux's usage. InputTokens excludes cached input, which is
// counted apart in CacheRead.
type wireUsage struct {
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CacheRead       *int64 `json:"cache_read_input_tokens,omitempty"`
	CacheWrite      *int64 `json:"cache_write_input_tokens,omitempty"`
	ReasoningTokens int64  `json:"reasoning_tokens,omitempty"`
}

type wireSubagentStarted struct {
	Agent struct {
		Name string `json:"name"`
	} `json:"agent"`
	Task  string   `json:"task"`
	Tools []string `json:"tools"`
	Model string   `json:"model"`
}

type wireStatus struct {
	Status     string `json:"status"`
	StopReason string `json:"stop_reason,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type wireSessionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type wireAttachment struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Data      []byte `json:"data"`
}

type wireEffort struct {
	Levels []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Available bool   `json:"available"`
		Next      struct {
			Price *struct {
				Per    int64  `json:"per"`
				Input  string `json:"input"`
				Output string `json:"output"`
			} `json:"price,omitempty"`
		} `json:"next"`
		Models []struct {
			Name      string `json:"name"`
			Charge    string `json:"charge"`
			Available bool   `json:"available"`
		} `json:"models"`
	} `json:"levels"`
}

type wireCatalogModel struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Disabled bool `json:"disabled,omitempty"`
	} `json:"spec"`
	Status struct {
		Available bool `json:"available"`
	} `json:"status"`
}

type wireStorageEntry struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Checksum string `json:"checksum"`
}

// platform is the set of platform calls the runtime makes, each acting as
// the person whose bearer it is given.
type platform struct{ client *client }

func (p platform) applyAgent(ctx context.Context, bearer, name string, manifest any) (int, error) {
	var out struct {
		Status struct {
			Version int `json:"version"`
		} `json:"status"`
	}
	if err := p.client.call(ctx, bearer, http.MethodPut, "/v1/agents/agents/"+escape(name), manifest, "", &out); err != nil {
		return 0, err
	}
	return out.Status.Version, nil
}

type createSession struct {
	Agent    string            `json:"agent"`
	Title    string            `json:"title,omitempty"`
	Attended bool              `json:"attended"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (p platform) createSession(ctx context.Context, bearer string, body createSession, idempotencyKey string) (wireSession, error) {
	var out wireSession
	err := p.client.call(ctx, bearer, http.MethodPost, "/v1/agents/sessions", body, idempotencyKey, &out)
	return out, err
}

func (p platform) session(ctx context.Context, bearer, id string) (wireSession, error) {
	var out wireSession
	err := p.client.call(ctx, bearer, http.MethodGet, "/v1/agents/sessions/"+escape(id), nil, "", &out)
	return out, err
}

// message sends a user.message. The idempotency key makes a send replayed
// after a crash resolve to the event the first send appended.
func (p platform) message(ctx context.Context, bearer, id, text string, attachments []wireAttachment, idempotencyKey string) (wireEvent, error) {
	payload := map[string]any{"content": []wireBlock{{Type: "text", Text: text}}}
	if len(attachments) > 0 {
		payload["attachments"] = attachments
	}
	var out wireEvent
	err := p.client.call(ctx, bearer, http.MethodPost, "/v1/agents/sessions/"+escape(id)+"/events",
		map[string]any{"type": "user.message", "payload": payload}, idempotencyKey, &out)
	return out, err
}

// markCancelled records the cancellation on the session itself, beside the
// metadata the session was created with, so it is seen by every process
// that polls the session.
func (p platform) markCancelled(ctx context.Context, bearer, id string, product Product) error {
	body := map[string]any{"metadata": map[string]string{"product": product.Key, product.cancelKey(): "1"}}
	return p.client.call(ctx, bearer, http.MethodPatch, "/v1/agents/sessions/"+escape(id), body, "", nil)
}

func (s wireSession) metadataCancelled(product Product) bool {
	return s.Metadata[product.cancelKey()] == "1"
}

func (p platform) interrupt(ctx context.Context, bearer, id string) error {
	return p.client.call(ctx, bearer, http.MethodPost, "/v1/agents/sessions/"+escape(id)+"/events",
		map[string]any{"type": "user.interrupt", "payload": map[string]any{}}, "interrupt-"+id, nil)
}

func (p platform) remove(ctx context.Context, bearer, id string) error {
	return p.client.call(ctx, bearer, http.MethodDelete, "/v1/agents/sessions/"+escape(id), nil, "", nil)
}

// events reads every event after fromSeq, following the cursor.
func (p platform) events(ctx context.Context, bearer, id string, fromSeq uint64) ([]wireEvent, error) {
	var all []wireEvent
	cursor := ""
	for {
		query := url.Values{"limit": {"500"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		} else {
			query.Set("from_seq", strconv.FormatUint(fromSeq+1, 10))
		}
		var page struct {
			Items      []wireEvent `json:"items"`
			NextCursor string      `json:"next_cursor"`
		}
		if err := p.client.call(ctx, bearer, http.MethodGet, "/v1/agents/sessions/"+escape(id)+"/events?"+query.Encode(), nil, "", &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

func (p platform) networkPreference(ctx context.Context, bearer string) (string, error) {
	var out struct {
		Network struct {
			Mode string `json:"mode"`
		} `json:"network"`
	}
	if err := p.client.call(ctx, bearer, http.MethodGet, "/v1/account/preferences", nil, "", &out); err != nil {
		return "", err
	}
	return out.Network.Mode, nil
}

func (p platform) effort(ctx context.Context, bearer string) (wireEffort, error) {
	var out wireEffort
	err := p.client.call(ctx, bearer, http.MethodGet, "/v1/account/effort", nil, "", &out)
	return out, err
}

// catalog lists every model the person may use, following the cursor.
func (p platform) catalog(ctx context.Context, bearer string) ([]wireCatalogModel, error) {
	var all []wireCatalogModel
	cursor := ""
	for range 100 {
		path := "/v1/models/models?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Items      []wireCatalogModel `json:"items"`
			NextCursor string             `json:"next_cursor"`
		}
		if err := p.client.call(ctx, bearer, http.MethodGet, path, nil, "", &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		cursor = page.NextCursor
	}
	return nil, fmt.Errorf("the model catalog did not end after 100 pages")
}

// storageList lists every file under a folder of the owner's Storage space,
// following the listing's cursor: Storage answers a page at a time, and a
// working directory with an installed library in it holds more files than
// one page.
func (p platform) storageList(ctx context.Context, bearer, owner, folder string) ([]wireStorageEntry, error) {
	var all []wireStorageEntry
	cursor := ""
	for range 1000 {
		query := url.Values{"list": {"1"}, "limit": {strconv.Itoa(storagePage)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var page struct {
			Entries    []wireStorageEntry `json:"entries"`
			NextCursor string             `json:"next_cursor"`
		}
		if err := p.client.call(ctx, bearer, http.MethodGet, "/v1/storage/files/"+escape(owner)+"/"+folder+"?"+query.Encode(), nil, "", &page); err != nil {
			return nil, err
		}
		all = append(all, page.Entries...)
		if page.NextCursor == "" || len(page.Entries) == 0 {
			return all, nil
		}
		cursor = page.NextCursor
	}
	return nil, fmt.Errorf("the session's folder did not end after 1000 pages")
}

// storagePage is how many entries one Storage listing asks for.
const storagePage = 500

func (p platform) storageRead(ctx context.Context, bearer, owner, path string) ([]byte, error) {
	return p.client.send(ctx, bearer, http.MethodGet, "/v1/storage/files/"+escape(owner)+"/"+path, nil, "")
}
