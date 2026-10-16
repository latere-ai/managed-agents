// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestGoldenValuesRoundTrip decodes the golden wire values into the generated
// Go types and encodes them again. The schema tests prove the JSON satisfies
// the schema; this proves the Go types agree with the same JSON, which is the
// half that would break if a field were renamed, dropped, or given the wrong
// optionality.
func TestGoldenValuesRoundTrip(t *testing.T) {
	cases := map[string]func() any{
		"SessionSpec":  func() any { return new(SessionSpec) },
		"SessionRef":   func() any { return new(SessionRef) },
		"Status":       func() any { return new(Status) },
		"Event":        func() any { return new(Event) },
		"Artifact":     func() any { return new(Artifact) },
		"Capabilities": func() any { return new(Capabilities) },
		"Model":        func() any { return new(Model) },
	}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if len(entries) != len(cases) {
		t.Fatalf("testdata holds %d files, the table covers %d", len(entries), len(cases))
	}
	for name, newValue := range cases {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("testdata", name+".json"))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			value := newValue()
			decoder := json.NewDecoder(bytes.NewReader(body))
			// An unknown field means the golden value carries something the Go
			// type does not, which is exactly the drift this test is for.
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(value); err != nil {
				t.Fatalf("decode: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			var before, after any
			if err := json.Unmarshal(body, &before); err != nil {
				t.Fatalf("parse golden: %v", err)
			}
			if err := json.Unmarshal(encoded, &after); err != nil {
				t.Fatalf("parse re-encoded: %v", err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("round trip changed the value:\nbefore %s\nafter  %s", body, encoded)
			}
		})
	}
}

// TestSessionSpecGoldenIsValid ties the wire form to the semantic rules: the
// golden specification is not merely well-shaped, it is one the port accepts.
func TestSessionSpecGoldenIsValid(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "SessionSpec.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var spec SessionSpec
	if err := json.Unmarshal(body, &spec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("the golden specification does not validate: %v", err)
	}
}
