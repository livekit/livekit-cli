//go:build ignore

// Command normalize rewrites an OpenAPI 3.1 spec so Go generators (oapi-codegen,
// ogen) can consume it: it collapses JSON-Schema union `type` arrays — which
// grpc-gateway emits for nullable fields ([X,"null"]) and 64-bit ints
// ([integer,string], sent as strings over protojson) — into a single 3.0-style
// scalar type plus `nullable: true` where applicable.
//
// Usage: go run normalize.go <in.yaml> <out.yaml>
package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: normalize <in> <out>")
		os.Exit(2)
	}
	in, err := os.ReadFile(os.Args[1])
	must(err)
	var doc any
	must(yaml.Unmarshal(in, &doc))
	walk(doc)
	out, err := yaml.Marshal(doc)
	must(err)
	must(os.WriteFile(os.Args[2], out, 0o644))
}

func walk(n any) {
	switch v := n.(type) {
	case map[string]any:
		if t, ok := v["type"].([]any); ok {
			collapseType(v, t)
		}
		for _, child := range v {
			walk(child)
		}
	case []any:
		for _, child := range v {
			walk(child)
		}
	}
}

// collapseType rewrites a union `type: [...]` on schema m into a single scalar
// type, recording nullability separately.
func collapseType(m map[string]any, types []any) {
	var nonNull []string
	hasNull := false
	for _, t := range types {
		s, _ := t.(string)
		if s == "null" {
			hasNull = true
			continue
		}
		nonNull = append(nonNull, s)
	}
	if hasNull {
		m["nullable"] = true
	}
	switch len(nonNull) {
	case 1:
		m["type"] = nonNull[0]
	case 0:
		m["type"] = "string"
	default:
		// A real union (e.g. [integer, string] for a 64-bit int sent as a
		// string): collapse to string, the protojson wire representation.
		m["type"] = "string"
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "normalize:", err)
		os.Exit(1)
	}
}
