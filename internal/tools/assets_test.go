package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginAndSkill_AreThinLocalAndSafe(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{".codex-plugin/plugin.json", ".mcp.json"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == ".codex-plugin/plugin.json" && document["mcpServers"] != "./.mcp.json" {
			t.Fatal("plugin does not reference MCP manifest")
		}
	}
	skill, err := os.ReadFile(filepath.Join(root, "skills", "prompt-better", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(skill)
	for _, required := range []string{"smallest applicable tool", "explicit consent", "Never paste broad conversation history", "stop at the requested boundary"} {
		if !strings.Contains(text, required) {
			t.Fatalf("skill invariant missing: %s", required)
		}
	}
	for _, forbidden := range []string{"api.openai.com", "http://", "https://localhost", "permissions =", "model ="} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("skill contains forbidden behavior: %s", forbidden)
		}
	}
}
