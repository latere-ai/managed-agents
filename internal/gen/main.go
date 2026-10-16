// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command gen writes the Go form of the managed-agent contract from its JSON
// Schema.
//
// The schema is the contract; this program is the only thing that turns it into
// Go, so a shape that is not in the schema cannot appear in the types. It reads
// one schema document and writes one Go file:
//
//	go -C internal/gen run . -schema ../../schema/managed.json -out ../../types.gen.go
//
// # The annotations
//
// Four things a general-purpose generator cannot infer are declared as x-
// keywords in the schema:
//
//   - x-type-order, on the root, lists the $defs in the order they should be
//     emitted. A JSON object has no order and alphabetical output would scatter
//     related types.
//   - x-field-order, on an object, does the same for its properties.
//   - x-go-name overrides the derived Go identifier: id becomes ID, sha256
//     becomes SHA256, url becomes URL.
//   - x-go-type names a Go type verbatim, for the two places a structural type
//     would be wrong (json.RawMessage, time.Time) and for the two enums whose
//     Go form is hand-written (State, Grade).
//   - x-go-key-type types a map's key, so a meter map is map[Meter]int64
//     rather than map[string]int64.
//   - x-go-open, with x-go-consts, declares a string type whose named values
//     are documentation rather than validation: a runtime-native value passes
//     through unchanged instead of being rejected.
//
// # Pointers
//
// Whether a field is a pointer is derived, not declared. A property that is not
// in the object's required list and whose type is an object becomes a pointer,
// because that is the only way to tell "absent" from "present and zero" for a
// struct. Everything else absent takes omitempty and its zero value.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

func main() {
	log.SetFlags(0)
	schemaPath := flag.String("schema", "", "path to the JSON Schema document")
	outPath := flag.String("out", "", "path to the Go file to write")
	flag.Parse()
	if *schemaPath == "" || *outPath == "" {
		log.Fatal("gen: -schema and -out are both required")
	}
	source, err := os.ReadFile(*schemaPath)
	if err != nil {
		log.Fatalf("gen: %v", err)
	}
	var root jsonschema.Schema
	if err := json.Unmarshal(source, &root); err != nil {
		log.Fatalf("gen: parse %s: %v", *schemaPath, err)
	}
	rendered, err := generate(&root, *schemaPath)
	if err != nil {
		log.Fatalf("gen: %v", err)
	}
	if err := os.WriteFile(*outPath, rendered, 0o644); err != nil {
		log.Fatalf("gen: %v", err)
	}
}

// generator accumulates the output and the imports the output turned out to
// need, so the import block is written from what was emitted rather than from a
// guess made before emitting.
type generator struct {
	root    *jsonschema.Schema
	body    bytes.Buffer
	imports map[string]bool
}

// notice is the license notice every source file in this repository opens
// with, the generated one included: it is part of the work, and the license
// gate reads the first two lines of every Go file.
const notice = "// SPDX-FileCopyrightText: 2026 Latere AI\n// SPDX-License-Identifier: Apache-2.0\n\n"

func generate(root *jsonschema.Schema, schemaPath string) ([]byte, error) {
	g := &generator{root: root, imports: map[string]bool{}}
	order, err := stringList(root.Extra, "x-type-order")
	if err != nil {
		return nil, err
	}
	if len(order) != len(root.Defs) {
		return nil, fmt.Errorf("x-type-order lists %d types, $defs holds %d", len(order), len(root.Defs))
	}
	for _, name := range order {
		def, ok := root.Defs[name]
		if !ok {
			return nil, fmt.Errorf("x-type-order names %q, which is not in $defs", name)
		}
		if err := g.emit(name, def); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}

	pkg, _ := root.Extra["x-go-package"].(string)
	if pkg == "" {
		pkg = "main"
	}
	var out bytes.Buffer
	out.WriteString(notice)
	fmt.Fprintf(&out, "// Code generated from %s. DO NOT EDIT.\n\n", trimDir(schemaPath))
	fmt.Fprintf(&out, "package %s\n", pkg)
	if len(g.imports) > 0 {
		paths := make([]string, 0, len(g.imports))
		for path := range g.imports {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		out.WriteString("\nimport (\n")
		for _, path := range paths {
			fmt.Fprintf(&out, "\t%q\n", path)
		}
		out.WriteString(")\n")
	}
	out.Write(g.body.Bytes())

	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w\n%s", err, out.String())
	}
	return formatted, nil
}

func (g *generator) emit(name string, def *jsonschema.Schema) error {
	switch {
	case def.Type == "object" && len(def.Properties) > 0:
		return g.emitStruct(name, def)
	case def.Type == "string":
		return g.emitStringType(name, def)
	default:
		return fmt.Errorf("unsupported $def shape (type %q)", def.Type)
	}
}

func (g *generator) emitStruct(name string, def *jsonschema.Schema) error {
	order, err := stringList(def.Extra, "x-field-order")
	if err != nil {
		return err
	}
	if len(order) != len(def.Properties) {
		return fmt.Errorf("x-field-order lists %d fields, properties holds %d", len(order), len(def.Properties))
	}
	required := map[string]bool{}
	for _, field := range def.Required {
		required[field] = true
	}

	g.body.WriteString("\n")
	g.body.WriteString(docComment(def.Description, ""))
	fmt.Fprintf(&g.body, "type %s struct {\n", goName(name, def))
	for _, jsonName := range order {
		property, ok := def.Properties[jsonName]
		if !ok {
			return fmt.Errorf("x-field-order names %q, which is not a property", jsonName)
		}
		goType, err := g.goType(property, required[jsonName])
		if err != nil {
			return fmt.Errorf("%s: %w", jsonName, err)
		}
		if property.Description != "" {
			g.body.WriteString(docComment(property.Description, "\t"))
		}
		tag := jsonName
		if !required[jsonName] {
			tag += ",omitempty"
		}
		fmt.Fprintf(&g.body, "\t%s %s `json:%q`\n", goName(jsonName, property), goType, tag)
	}
	g.body.WriteString("}\n")
	return nil
}

// emitStringType writes a named string type and, where the schema names them,
// its constants. A closed set states them under oneOf, so a validator in any
// language rejects an unknown value; an open set states them under x-go-consts
// with no oneOf, so an unknown value passes through. The difference is the
// whole point of the two spellings and is why the generator does not normalize
// them into one.
func (g *generator) emitStringType(name string, def *jsonschema.Schema) error {
	g.body.WriteString("\n")
	g.body.WriteString(docComment(def.Description, ""))
	fmt.Fprintf(&g.body, "type %s string\n", name)

	values := def.OneOf
	if len(values) == 0 {
		var open []*jsonschema.Schema
		raw, ok := def.Extra["x-go-consts"]
		if !ok {
			return nil
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("x-go-consts: %w", err)
		}
		if err := json.Unmarshal(encoded, &open); err != nil {
			return fmt.Errorf("x-go-consts: %w", err)
		}
		values = open
	}
	if len(values) == 0 {
		return nil
	}
	g.body.WriteString("\nconst (\n")
	for index, value := range values {
		if value.Const == nil {
			return fmt.Errorf("constant %d has no const", index)
		}
		literal, ok := (*value.Const).(string)
		if !ok {
			return fmt.Errorf("constant %d is not a string", index)
		}
		if value.Title == "" {
			return fmt.Errorf("constant %q has no title to name it in Go", literal)
		}
		if index > 0 {
			g.body.WriteString("\n")
		}
		g.body.WriteString(docComment(value.Description, "\t"))
		fmt.Fprintf(&g.body, "\t%s %s = %q\n", value.Title, name, literal)
	}
	g.body.WriteString(")\n")
	return nil
}

func (g *generator) goType(schema *jsonschema.Schema, required bool) (string, error) {
	if named, ok := schema.Extra["x-go-type"].(string); ok {
		switch named {
		case "json.RawMessage":
			g.imports["encoding/json"] = true
		case "time.Time":
			g.imports["time"] = true
		}
		return named, nil
	}
	if schema.Ref != "" {
		name, ok := strings.CutPrefix(schema.Ref, "#/$defs/")
		if !ok {
			return "", fmt.Errorf("unsupported $ref %q", schema.Ref)
		}
		target, ok := g.root.Defs[name]
		if !ok {
			return "", fmt.Errorf("$ref %q names no $def", schema.Ref)
		}
		// Only a struct needs a pointer to distinguish absent from zero. A
		// named string type is a string: its zero value already reads as absent
		// and a pointer would only add a nil check at every use.
		if target.Type == "object" && !required {
			return "*" + name, nil
		}
		return name, nil
	}
	switch schema.Type {
	case "string":
		return "string", nil
	case "boolean":
		return "bool", nil
	case "integer":
		switch schema.Format {
		case "int64":
			return "int64", nil
		case "uint64":
			return "uint64", nil
		case "":
			return "int", nil
		default:
			return "", fmt.Errorf("unsupported integer format %q", schema.Format)
		}
	case "array":
		if schema.Items == nil {
			return "", fmt.Errorf("array has no items")
		}
		element, err := g.goType(schema.Items, true)
		if err != nil {
			return "", err
		}
		return "[]" + element, nil
	case "object":
		if schema.AdditionalProperties == nil {
			return "", fmt.Errorf("object has neither properties nor additionalProperties")
		}
		value, err := g.goType(schema.AdditionalProperties, true)
		if err != nil {
			return "", err
		}
		key := "string"
		if named, ok := schema.Extra["x-go-key-type"].(string); ok {
			key = named
		}
		return fmt.Sprintf("map[%s]%s", key, value), nil
	default:
		return "", fmt.Errorf("unsupported type %q", schema.Type)
	}
}

// goName is the Go identifier for a schema name, honoring an x-go-name
// override. Without one it upper-cases each underscore-separated word, which is
// right for every name in the contract that does not contain an initialism —
// and the initialisms are exactly what the override exists for.
func goName(jsonName string, schema *jsonschema.Schema) string {
	if schema != nil {
		if override, ok := schema.Extra["x-go-name"].(string); ok {
			return override
		}
	}
	var out strings.Builder
	for word := range strings.SplitSeq(jsonName, "_") {
		if word == "" {
			continue
		}
		out.WriteString(strings.ToUpper(word[:1]))
		out.WriteString(word[1:])
	}
	return out.String()
}

// docComment wraps text into Go comment lines at the width the rest of the
// contract is written to, keeping blank-line paragraph breaks.
func docComment(text, indent string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	const width = 77
	var out strings.Builder
	for index, paragraph := range strings.Split(text, "\n\n") {
		if index > 0 {
			out.WriteString(indent + "//\n")
		}
		line := ""
		for word := range strings.FieldsSeq(paragraph) {
			switch {
			case line == "":
				line = word
			case len(indent)+len(line)+1+len(word) <= width:
				line += " " + word
			default:
				out.WriteString(indent + "// " + line + "\n")
				line = word
			}
		}
		if line != "" {
			out.WriteString(indent + "// " + line + "\n")
		}
	}
	return out.String()
}

func stringList(extra map[string]any, key string) ([]string, error) {
	raw, ok := extra[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not a list", key)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s holds a non-string", key)
		}
		out = append(out, value)
	}
	return out, nil
}

func trimDir(path string) string {
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}
