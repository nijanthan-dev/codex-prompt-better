package policypack

import (
	"reflect"
	"strings"
	"testing"
)

const validPackJSON = `{"schema_version":"1.0.0","id":"scope","version":"1.0.0","rules":[{"id":"requested","category":"scope","outcome":"continue","risk_score":10,"explanation":"Keep requested scope.","conditions":[{"field":"category","operator":"equals","value":"scope"}]}]}`

func TestParseAndEvaluateDeterministically(t *testing.T) {
	pack, err := Parse([]byte(validPackJSON))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Evaluate([]Pack{pack}, map[string]string{"category": "scope"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluate([]Pack{pack}, map[string]string{"category": "scope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || !reflect.DeepEqual(first, second) {
		t.Fatalf("matches=%+v", first)
	}
}

func TestParseRejectsUnknownUnboundedAndIncompatibleRules(t *testing.T) {
	tests := []string{
		strings.Replace(validPackJSON, `"outcome":"continue"`, `"outcome":"execute"`, 1),
		strings.Replace(validPackJSON, `"operator":"equals"`, `"operator":"regex"`, 1),
		strings.Replace(validPackJSON, `"field":"category"`, `"field":"action_class"`, 1),
		strings.Replace(validPackJSON, `"value":"scope"`, `"value":"unknown_action"`, 1),
		strings.Replace(validPackJSON, `"version":"1.0.0"`, `"version":"2.0.0"`, 1),
		strings.Replace(validPackJSON, `"rules":`, `"unknown":true,"rules":`, 1),
	}
	for _, input := range tests {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("unsafe policy pack accepted: %s", input)
		}
	}
	if _, err := Parse([]byte(strings.Repeat("x", MaxPackBytes+1))); err == nil {
		t.Fatal("oversized policy pack accepted")
	}
}

func TestValidateSetRejectsDuplicateAndExcessivePacks(t *testing.T) {
	pack, err := Parse([]byte(validPackJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSet([]Pack{pack, pack}); err == nil {
		t.Fatal("duplicate pack accepted")
	}
	packs := make([]Pack, MaxPacks+1)
	for index := range packs {
		packs[index] = pack
		packs[index].ID = "pack-" + string(rune('a'+index))
	}
	if err := ValidateSet(packs); err == nil {
		t.Fatal("excessive pack set accepted")
	}
}

func TestValidateExtensionsRejectsBroadeningRules(t *testing.T) {
	pack, err := Parse([]byte(validPackJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateExtensions([]Pack{pack}); err == nil {
		t.Fatal("broadening extension accepted")
	}
	pack.Rules[0].Outcome = "warn"
	if err := ValidateExtensions([]Pack{pack}); err != nil {
		t.Fatalf("narrowing extension rejected: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(validPackJSON))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}
