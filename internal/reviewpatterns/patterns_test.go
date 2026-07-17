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

func TestValidateTreeRequiresReleaseGuardrails(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string]string{
		"AGENTS.md":                            ".agents/skills/verify-release-packaging/SKILL.md\n",
		"README.md":                            "Go 1.25.12+\n",
		"docs/installation.md":                 "Go 1.25.12 or newer\n",
		"go.mod":                               "module example.com/test\n\ngo 1.25.12\n",
		".dockerignore":                        ".git\n",
		".act/Dockerfile":                      "FROM golang:1.25.12-bookworm@sha256:test\nFROM postgres:17-bookworm@sha256:test\nRUN curl https://vuln.go.dev/vulndb.zip\nRUN unzip -Z1 /tmp/vulndb.zip\n",
		".act/workflows/ci.yml":                "run: govulncheck -db=file:///opt/go-vulndb ./...\n",
		".github/workflows/release-please.yml": "go-version-file: go.mod\n",
		"internal/setup/setup.go":              "package setup\n// patch >= 12\n",
		"scripts/run-local-ci.sh":              "cleanup_stale() { :; }\ndev.prompt-better.local-ci\npostgres:16-bookworm@sha256:test\npostgres:17-bookworm@sha256:test\ndocker network create --internal x\nact --action-offline-mode\n",
		"scripts/install.sh":                   "lock_dir=$install_dir/.prompt-better-lock\nacquire_lock() { :; }\nacquire_lock\nacquire_lock\nacquire_lock\n",
		"scripts/verify-package-output.sh":     "tar -tzf \"$archive\"\ndarwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64\necho checksum manifest incomplete\ntar -xzf \"$archive\"\n",
	}
	for name, content := range fixtures {
		writeFixture(t, root, name, content)
	}
	failures, err := ValidateTree(root)
	if err != nil || len(failures) != 0 {
		t.Fatalf("valid release fixtures rejected: failures=%v err=%v", failures, err)
	}
	for _, test := range []struct {
		file, remove, want string
	}{
		{".act/Dockerfile", "@sha256:test", "Act Go version and digest"},
		{".act/Dockerfile", "unzip -Z1 /tmp/vulndb.zip", "preflight the vulnerability database"},
		{".act/workflows/ci.yml", "file:///opt/go-vulndb", "offline vulnerability database"},
		{"scripts/run-local-ci.sh", "--internal", "Act network must be internal"},
		{"scripts/run-local-ci.sh", "--action-offline-mode", "Act actions must be offline"},
		{"scripts/run-local-ci.sh", "dev.prompt-better.local-ci", "label and verify owned"},
		{"scripts/install.sh", "lock_dir=$install_dir/.prompt-better-lock", "lock installer mutations"},
		{"scripts/verify-package-output.sh", "tar -tzf \"$archive\"", "preflight must precede extraction"},
		{"scripts/verify-package-output.sh", "windows_amd64", "missing exact target windows_amd64"},
		{"scripts/verify-package-output.sh", "checksum manifest incomplete", "exact checksum coverage"},
	} {
		t.Run(test.want, func(t *testing.T) {
			writeFixture(t, root, test.file, strings.Replace(fixtures[test.file], test.remove, "", 1))
			defer writeFixture(t, root, test.file, fixtures[test.file])
			failures, err := ValidateTree(root)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(failures, "\n"), test.want) {
				t.Fatalf("missing %q diagnostic: %v", test.want, failures)
			}
		})
	}
}

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
