// Package confounder preserves observable confounders without invention.
package confounder

var supported = map[string]bool{
	"model": true, "tokenizer": true, "host": true, "tool": true,
	"instruction": true, "config": true, "memory": true, "media": true,
}

type Observation struct {
	Kind       string
	Value      string
	Provenance string
}

func Normalize(kind, value, provenance string) Observation {
	if !supported[kind] || value == "" {
		return Observation{Kind: kind, Provenance: "unknown"}
	}
	if provenance != "observed" && provenance != "configured" {
		return Observation{Kind: kind, Provenance: "unknown"}
	}
	return Observation{Kind: kind, Value: value, Provenance: provenance}
}
