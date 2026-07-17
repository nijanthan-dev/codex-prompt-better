package reviewpatterns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTreeRequiresCompleteJSONConsumption(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "valid.go", `package fixture
import ("encoding/json"; "strings")
func valid() error { d := json.NewDecoder(strings.NewReader("{}")); var value any; if err := d.Decode(&value); err != nil { return err }; return d.Decode(&value) }
`)
	failures, err := ValidateTree(root)
	if err != nil || len(failures) != 0 {
		t.Fatalf("valid fixture rejected: failures=%v err=%v", failures, err)
	}
	writeFixture(t, root, "invalid.go", `package fixture
import ("encoding/json"; "strings")
type unrelated struct{}
func (unrelated) Decode(any) error { return nil }
func invalid() error { d := json.NewDecoder(strings.NewReader("{}")); var value any; _ = (unrelated{}).Decode(&value); return d.Decode(&value) }
`)
	failures, err = ValidateTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "reject trailing documents") {
		t.Fatalf("invalid fixture not diagnosed: %v", failures)
	}
}

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
