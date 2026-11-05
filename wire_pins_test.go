// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
)

// TestOptionalFieldsAreLeftOutByTheirZeroValue pins the bytes of the wire
// types: a zero number, false and a nil raw value are left out, a raw `{}`,
// `[]` or `null` is written, and a pointer is left out only when nil.
func TestOptionalFieldsAreLeftOutByTheirZeroValue(t *testing.T) {
	base := SessionRef{Runtime: "r", ID: "s", Revision: 1, LocatorVersion: 1}
	cases := []struct {
		name string
		ref  SessionRef
		want string
	}{
		{"no locator", base, `{"runtime":"r","id":"s","revision":1,"locator_version":1}`},
		{"an empty object", withLocator(base, `{}`), `{"runtime":"r","id":"s","revision":1,"locator_version":1,"locator":{}}`},
		{"an empty array", withLocator(base, `[]`), `{"runtime":"r","id":"s","revision":1,"locator_version":1,"locator":[]}`},
		{"a null", withLocator(base, `null`), `{"runtime":"r","id":"s","revision":1,"locator_version":1,"locator":null}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.ref)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	caps, err := json.Marshal(Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal(caps, &members); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"resumable", "cancellation", "artifacts", "evaluation", "max_locator_bytes", "max_attachment_bytes"} {
		if _, ok := members[absent]; ok {
			t.Errorf("a zero %s is written: %s", absent, caps)
		}
	}
	on, err := json.Marshal(Capabilities{Resumable: true, MaxLocatorBytes: 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(on, &members); err != nil || members["resumable"] != true || members["max_locator_bytes"] != float64(7) {
		t.Errorf("a set capability is not written: %s", on)
	}
}

func withLocator(ref SessionRef, locator string) SessionRef {
	ref.Locator = jsontext.Value(locator)
	return ref
}

// TestEnumsAreNamesOnTheWire pins the enums' bytes, the case where a
// custom marshaler sits under an omitted field.
func TestEnumsAreNamesOnTheWire(t *testing.T) {
	got, err := json.Marshal([]any{StatePending, GradeNeedsRevision})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `["pending","needs_revision"]` {
		t.Fatalf("enums encode as %s", got)
	}
}

// TestALocatorWithARepeatedMemberIsValid pins that a locator a runtime wrote
// with a repeated member name or invalid UTF-8 is still accepted.
func TestALocatorWithARepeatedMemberIsValid(t *testing.T) {
	for _, locator := range []string{`{"a":1,"a":2}`, "{\"a\":\"\xff\"}"} {
		ref := SessionRef{Runtime: "r", ID: "s", Revision: 1, LocatorVersion: 1, Locator: jsontext.Value(locator)}
		if err := ref.Validate(0); err != nil {
			t.Errorf("locator %q refused: %v", locator, err)
		}
	}
}
