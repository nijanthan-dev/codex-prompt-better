package reviewpatterns

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var goDirective = regexp.MustCompile(`(?m)^go ([0-9]+\.[0-9]+\.[0-9]+)$`)

func validateRelease(root string) ([]string, error) {
	files := map[string]string{}
	for _, name := range []string{
		".act/Dockerfile", ".act/workflows/ci.yml", ".dockerignore", ".github/workflows/release-please.yml",
		"AGENTS.md", "README.md", "docs/installation.md", "go.mod", "internal/setup/setup.go",
		"scripts/install.sh", "scripts/run-local-ci.sh", "scripts/verify-package-output.sh",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		files[name] = string(data)
	}
	if len(files) == 0 {
		return nil, nil
	}

	var failures []string
	require := func(file, value, message string) {
		if !strings.Contains(files[file], value) {
			failures = append(failures, fmt.Sprintf("%s: %s", file, message))
		}
	}

	match := goDirective.FindStringSubmatch(files["go.mod"])
	if len(match) != 2 {
		failures = append(failures, "go.mod: require a full Go patch version")
	} else {
		require(".act/Dockerfile", "FROM golang:"+match[1]+"-bookworm@sha256:", "Act Go version and digest must match go.mod")
		require("README.md", "Go "+match[1]+"+", "document the exact supported Go patch")
		require("docs/installation.md", "Go "+match[1]+" or newer", "document the exact supported Go patch")
	}
	require("AGENTS.md", ".agents/skills/verify-release-packaging/SKILL.md", "require the repository release skill")
	require(".dockerignore", ".git", "Docker context must exclude Git metadata")
	require(".act/Dockerfile", "https://vuln.go.dev/vulndb.zip", "preload the Go vulnerability database")
	require(".act/Dockerfile", "unzip -Z1 /tmp/vulndb.zip", "preflight the vulnerability database before extraction")
	require(".act/Dockerfile", "FROM postgres:17-bookworm@sha256:", "pin the Act PostgreSQL base by digest")
	require(".act/workflows/ci.yml", "govulncheck -db=file:///opt/go-vulndb", "scan against the offline vulnerability database")
	require(".github/workflows/release-please.yml", "go-version-file: go.mod", "release Go version must come from go.mod")
	require("internal/setup/setup.go", "patch >= 12", "runtime readiness must reject vulnerable Go patches")
	require("scripts/run-local-ci.sh", "docker network create --internal", "Act network must be internal")
	require("scripts/run-local-ci.sh", "--action-offline-mode", "Act actions must be offline")
	require("scripts/run-local-ci.sh", "cleanup_stale", "clean resources owned by dead validation processes")
	require("scripts/run-local-ci.sh", "dev.prompt-better.local-ci", "label and verify owned Docker resources")
	require("scripts/run-local-ci.sh", "postgres:16-bookworm@sha256:", "pin PostgreSQL 16 integration by digest")
	require("scripts/run-local-ci.sh", "postgres:17-bookworm@sha256:", "pin PostgreSQL 17 integration by digest")
	require("scripts/install.sh", `lock_dir=$install_dir/.prompt-better-lock`, "lock installer mutations by destination")
	if strings.Count(files["scripts/install.sh"], "acquire_lock") < 4 {
		failures = append(failures, "scripts/install.sh: install, rollback, and uninstall must acquire the mutation lock")
	}

	verifier := files["scripts/verify-package-output.sh"]
	listAt := strings.Index(verifier, `tar -tzf "$archive"`)
	extractAt := strings.Index(verifier, `tar -xzf "$archive"`)
	if listAt < 0 || extractAt < 0 || listAt > extractAt {
		failures = append(failures, "scripts/verify-package-output.sh: archive preflight must precede extraction")
	}
	for _, target := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64"} {
		if !strings.Contains(verifier, target) {
			failures = append(failures, "scripts/verify-package-output.sh: missing exact target "+target)
		}
	}
	require("scripts/verify-package-output.sh", "checksum manifest incomplete", "enforce exact checksum coverage")
	return failures, nil
}
