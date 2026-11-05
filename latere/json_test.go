// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestTheSpecDigestInputIsFixed pins the bytes an agent's name hashes: a nil
// list is null, the HTML characters and the line separator are escaped, and
// the members keep the struct's order.
func TestTheSpecDigestInputIsFixed(t *testing.T) {
	spec := agentSpec{Instructions: "a <b> & c\u2028", Permissions: []map[string]string{{"z": "1", "a": "2"}}}
	got, err := encodeSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":{"name":""},"instructions":"a \u003cb\u003e \u0026 c\u2028","tools":null,` +
		`"permissions":[{"a":"2","z":"1"}],"approvals":{"mode":""},"subagentLimits":{},` +
		`"machine":{"kind":""},"limits":{"turnTimeout":"","maxAge":""},"context":{}}`
	if string(got) != want {
		t.Fatalf("spec encodes as\n%s\nwant\n%s", got, want)
	}
}

// TestALocatorIsFixedBytes pins the locator a reference stores.
func TestALocatorIsFixedBytes(t *testing.T) {
	ref, err := reference("s1", 1, locator{Agent: "a<b", Model: "m", Started: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"agent":"a\u003cb","model":"m","graded":false,"task_seq":0,"started":"2026-01-02T03:04:05Z"}`
	if string(ref.Locator) != want {
		t.Fatalf("locator = %s, want %s", ref.Locator, want)
	}
	if strings.Contains(string(ref.Locator), "max_revisions") {
		t.Fatalf("a zero revision budget is written: %s", ref.Locator)
	}
}

// TestARequestBodyIsStable checks that a body with several map members is the
// same bytes on every send, and that text that is not valid UTF-8 is sent with
// the replacement character.
func TestARequestBodyIsStable(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	body := map[string]any{"d": 1, "c": "x\xffy", "b": []string{"<"}, "a": map[string]int{"z": 1, "y": 2}}
	for range 8 {
		if err := c.call(context.Background(), "t", http.MethodPost, "/x", body, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	want := "{\"" + "a\":{\"y\":2,\"z\":1},\"b\":[\"<\"],\"c\":\"x\ufffdy\",\"d\":1}"
	for _, got := range bodies {
		if got != want {
			t.Fatalf("body = %q, want %q", got, want)
		}
	}
}

// TestForeignJSONIsRead checks that what a model or a tool wrote is read as
// before: a repeated member takes the last value, a member name in another
// case matches, and a string with invalid UTF-8 reads with the replacement
// character.
func TestForeignJSONIsRead(t *testing.T) {
	verdict, err := lastVerdictObject("reasoning {\"grade\":\"passed\",\"Grade\":\"failed\",\"explanation\":\"bad\xff\",\"criteria\":[]}")
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Grade != "failed" || verdict.Explanation != "bad\ufffd" {
		t.Fatalf("verdict = %+v", verdict)
	}
	payload := "{\"message\":{\"blocks\":[{\"type\":\"text\",\"text\":\"a\xff\",\"Text\":\"dup\"}]}}"
	got := normalize([]wireEvent{{ID: "e1", Type: "agent.message", Payload: []byte(payload)}})
	if len(got) != 1 || got[0].Content != "dup" {
		t.Fatalf("normalized = %+v", got)
	}
}

// TestARequestBodyWritesNilListsAndMapsAsNull pins the bytes sent to the
// platform for a nil slice and map.
func TestARequestBodyWritesNilListsAndMapsAsNull(t *testing.T) {
	got, err := json.Marshal(struct {
		L []string          `json:"l"`
		M map[string]string `json:"m"`
	}{}, sentJSON)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"l":null,"m":null}` {
		t.Fatalf("body = %s", got)
	}
}
