package builtin

import (
	"embed"
	"fmt"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policypack"
)

//go:embed *.json
var files embed.FS

var orderedFiles = []string{
	"01-repository-worktree.json",
	"02-instructions-scope-non-goals.json",
	"03-generated-vendor.json",
	"04-validation-release.json",
	"05-privacy-security.json",
	"06-tool-retrieval-ptc.json",
	"07-autonomy-fallback-stopping-delegation.json",
}

func Load() ([]policypack.Pack, error) {
	packs := make([]policypack.Pack, 0, len(orderedFiles))
	for _, name := range orderedFiles {
		data, err := files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read embedded policy pack: %w", err)
		}
		pack, err := policypack.Parse(data)
		if err != nil {
			return nil, err
		}
		packs = append(packs, pack)
	}
	if err := policypack.ValidateSet(packs); err != nil {
		return nil, err
	}
	return packs, nil
}
