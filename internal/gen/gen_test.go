package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	schemaPath  = "../../schema/managed.json"
	generated   = "../../types.gen.go"
	goldenDir   = "../../testdata"
	goldenSuffx = ".json"
)

func loadSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	source, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var root jsonschema.Schema
	if err := json.Unmarshal(source, &root); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return &root
}

// TestGeneratedFileIsUpToDate is the guard that keeps the schema the source of
// truth. Editing the schema and forgetting to regenerate would otherwise leave
// Go and JSON disagreeing about the contract, with each looking authoritative
// on its own.
func TestGeneratedFileIsUpToDate(t *testing.T) {
	rendered, err := generate(loadSchema(t), schemaPath)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	committed, err := os.ReadFile(generated)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if string(rendered) != string(committed) {
		t.Fatalf("%s is stale; run go generate ./...", generated)
	}
}

// TestGoldenValuesValidateAgainstTheSchema checks the schema against real
// values rather than only against itself. A schema nothing is validated with is
// a document, not a contract, and this is what would catch a required list or a
// pattern that describes a shape the port does not actually emit.
func TestGoldenValuesValidateAgainstTheSchema(t *testing.T) {
	root := loadSchema(t)
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatalf("read golden directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no golden values to validate")
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), goldenSuffx)
		if name == entry.Name() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if _, ok := root.Defs[name]; !ok {
				t.Fatalf("golden file names %q, which is not a $def", name)
			}
			// The document's own root is SessionSpec, so validating any other
			// type means pointing a fresh root at that $def and carrying the
			// same $defs across for the references inside it to resolve.
			wrapper := &jsonschema.Schema{Ref: "#/$defs/" + name, Defs: root.Defs}
			resolved, err := wrapper.Resolve(nil)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			body, err := os.ReadFile(filepath.Join(goldenDir, entry.Name()))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			var value any
			if err := json.Unmarshal(body, &value); err != nil {
				t.Fatalf("parse golden: %v", err)
			}
			if err := resolved.Validate(value); err != nil {
				t.Fatalf("golden value does not satisfy its schema: %v", err)
			}
		})
	}
}

// TestSchemaRejectsWhatTheContractForbids proves the schema carries the
// structural half of the rules rather than accepting anything shaped roughly
// right.
func TestSchemaRejectsWhatTheContractForbids(t *testing.T) {
	root := loadSchema(t)
	cases := []struct {
		name  string
		def   string
		value string
	}{
		{"unknown network mode", "NetworkPolicy", `{"mode":"partial"}`},
		{"unknown access mode", "AccessMode", `"append"`},
		{"unknown resource kind", "ResourceKind", `"dataset"`},
		{"unknown role", "Role", `"tool"`},
		{"unknown preference", "ModelSelector", `{"preference":"cheapest"}`},
		{"model with no id", "ModelRef", `{"provider":"reference"}`},
		{"revision below one", "SessionRef", `{"runtime":"reference","id":"s","revision":0,"locator_version":1}`},
		{"non-canonical runtime name", "SessionRef", `{"runtime":"Reference","id":"s","revision":1,"locator_version":1}`},
		// The Go half rejects this too. A name that is its own lower-cased,
		// trimmed self used to pass there while failing here, which is the kind
		// of disagreement "one contract, two forms" is supposed to make
		// impossible.
		{"runtime name with a space", "SessionRef", `{"runtime":"my runtime","id":"s","revision":1,"locator_version":1}`},
		{"sha256 that is not a digest", "AttachmentResource", `{"path":"a","content":"","sha256":"nope"}`},
		{"negative token bucket", "Usage", `{"model":{"provider":"p","id":"i"},"input_fresh":-1,"input_cached":0,"cache_write":0,"cache_write_5m":0,"cache_write_1h":0,"output":0}`},
		{"evaluation with no revisions", "EvaluationSpec", `{"rubric":"r","max_revisions":0}`},
		{"unknown property", "Artifact", `{"id":"a","path":"p","extra":1}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			wrapper := &jsonschema.Schema{Ref: "#/$defs/" + testCase.def, Defs: root.Defs}
			resolved, err := wrapper.Resolve(nil)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			var value any
			if err := json.Unmarshal([]byte(testCase.value), &value); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := resolved.Validate(value); err == nil {
				t.Fatal("schema accepted a value the contract forbids")
			}
		})
	}
}

// TestEventKindStaysOpen is the counterpart: EventKind names four kinds and
// must still accept a runtime's own, because the transcript is evidence and
// coercing an unknown kind into a neighbour would falsify it.
func TestEventKindStaysOpen(t *testing.T) {
	root := loadSchema(t)
	wrapper := &jsonschema.Schema{Ref: "#/$defs/EventKind", Defs: root.Defs}
	resolved, err := wrapper.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := resolved.Validate("model_request_end"); err != nil {
		t.Fatalf("EventKind rejected a runtime-native kind: %v", err)
	}
}

func TestGeneratorRejectsAMalformedSchema(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no type order", `{"$defs":{}}`},
		{"type order disagrees with defs", `{"x-type-order":["A"],"$defs":{}}`},
		{"type order names a missing def", `{"x-type-order":["A"],"$defs":{"B":{"type":"string"}}}`},
		{"unsupported def shape", `{"x-type-order":["A"],"$defs":{"A":{"type":"integer"}}}`},
		{"field order disagrees with properties", `{"x-type-order":["A"],"$defs":{"A":{"type":"object","x-field-order":[],"properties":{"a":{"type":"string"}}}}}`},
		{"field order names a missing property", `{"x-type-order":["A"],"$defs":{"A":{"type":"object","x-field-order":["b"],"properties":{"a":{"type":"string"}}}}}`},
		{"unsupported integer format", `{"x-type-order":["A"],"$defs":{"A":{"type":"object","x-field-order":["a"],"properties":{"a":{"type":"integer","format":"int128"}}}}}`},
		{"array with no items", `{"x-type-order":["A"],"$defs":{"A":{"type":"object","x-field-order":["a"],"properties":{"a":{"type":"array"}}}}}`},
		{"open map with no value type", `{"x-type-order":["A"],"$defs":{"A":{"type":"object","x-field-order":["a"],"properties":{"a":{"type":"object"}}}}}`},
		{"reference to a missing def", `{"x-type-order":["A"],"$defs":{"A":{"type":"object","x-field-order":["a"],"properties":{"a":{"$ref":"#/$defs/Missing"}}}}}`},
		{"constant with no title", `{"x-type-order":["A"],"$defs":{"A":{"type":"string","oneOf":[{"const":"x"}]}}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var root jsonschema.Schema
			if err := json.Unmarshal([]byte(testCase.body), &root); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := generate(&root, "test.json"); err == nil {
				t.Fatal("generator accepted a malformed schema")
			}
		})
	}
}

func TestGeneratorEmitsAnOpenEnum(t *testing.T) {
	body := `{"x-go-package":"p","x-type-order":["Kind"],"$defs":{"Kind":{"type":"string",
	  "description":"Kind is open.","x-go-open":true,
	  "x-go-consts":[{"const":"a","title":"KindA","description":"KindA is a."}]}}}`
	var root jsonschema.Schema
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := generate(&root, "test.json")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, want := range []string{"type Kind string", `KindA Kind = "a"`, "// KindA is a."} {
		if !strings.Contains(string(out), want) {
			t.Errorf("generated source is missing %q:\n%s", want, out)
		}
	}
}
