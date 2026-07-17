package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallUpgradeRollbackUninstallPreservesUserData(t *testing.T) {
	root := repositoryRoot(t)
	temp := t.TempDir()
	release := filepath.Join(temp, "release")
	tools := filepath.Join(temp, "tools")
	home := filepath.Join(temp, "home")
	install := filepath.Join(temp, "bin")
	state := filepath.Join(temp, "state")
	mustMkdir(t, release, tools, home)
	writeFakeReleaseTools(t, tools)

	private := filepath.Join(home, ".codex", "synthetic-state")
	mustMkdir(t, filepath.Dir(private))
	if err := os.WriteFile(private, []byte("preserve-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	writeReleaseFixture(t, release, "1.2.3", false)
	runInstaller(t, root, tools, release, home, state, "", "--version", "v1.2.3", "--install-dir", install)
	assertInstalledVersion(t, install, "v1.2.3")

	writeReleaseFixture(t, release, "1.2.4", false)
	runInstaller(t, root, tools, release, home, state, "", "--version", "v1.2.4", "--install-dir", install)
	assertInstalledVersion(t, install, "v1.2.4")
	runInstaller(t, root, tools, release, home, state, "", "--rollback", "--install-dir", install)
	assertInstalledVersion(t, install, "v1.2.3")
	runInstaller(t, root, tools, release, home, state, "", "--uninstall", "--install-dir", install)
	for _, name := range packageBinaries() {
		if _, err := os.Stat(filepath.Join(install, name)); !os.IsNotExist(err) {
			t.Fatalf("binary remains after uninstall: %s", name)
		}
	}
	data, err := os.ReadFile(private)
	if err != nil || string(data) != "preserve-me" {
		t.Fatalf("user data changed: %q %v", data, err)
	}
}

func TestInstallerFailsClosedOnProvenanceTamperAndUnownedDestination(t *testing.T) {
	root := repositoryRoot(t)
	for _, test := range []struct {
		name     string
		ghFail   string
		tamper   bool
		unsafe   string
		unowned  bool
		wantText string
	}{
		{name: "provenance", ghFail: "1", wantText: "provenance verification failed"},
		{name: "checksum", tamper: true, wantText: "checksum mismatch"},
		{name: "traversal", unsafe: "../escape", wantText: "unsafe archive path"},
		{name: "symlink", unsafe: "symlink", wantText: "non-regular entries"},
		{name: "unexpected", unsafe: "extra", wantText: "contents invalid"},
		{name: "unowned", unowned: true, wantText: "unowned binary"},
	} {
		t.Run(test.name, func(t *testing.T) {
			temp := t.TempDir()
			release := filepath.Join(temp, "release")
			tools := filepath.Join(temp, "tools")
			home := filepath.Join(temp, "home")
			install := filepath.Join(temp, "bin")
			mustMkdir(t, release, tools, home, install)
			writeFakeReleaseTools(t, tools)
			writeReleaseFixture(t, release, "1.2.3", test.tamper, test.unsafe)
			if test.unowned {
				if err := os.WriteFile(filepath.Join(install, "prompt-better"), []byte("unowned"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			output, err := installerCommand(root, tools, release, home, filepath.Join(temp, "state"), test.ghFail,
				"--version", "v1.2.3", "--install-dir", install).CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.wantText) {
				t.Fatalf("error=%v output=%s", err, output)
			}
			for _, name := range packageBinaries() {
				if test.unowned && name == "prompt-better" {
					continue
				}
				if _, err := os.Stat(filepath.Join(install, name)); !os.IsNotExist(err) {
					t.Fatalf("unexpected installed binary: %s", name)
				}
			}
		})
	}
}

func TestInstallerRestoresCompleteSetAfterPartialFailure(t *testing.T) {
	root := repositoryRoot(t)
	temp := t.TempDir()
	release := filepath.Join(temp, "release")
	tools := filepath.Join(temp, "tools")
	home := filepath.Join(temp, "home")
	install := filepath.Join(temp, "bin")
	state := filepath.Join(temp, "state")
	mustMkdir(t, release, tools, home)
	writeFakeReleaseTools(t, tools)
	writeReleaseFixture(t, release, "1.2.3", false)
	runInstaller(t, root, tools, release, home, state, "", "--version", "v1.2.3", "--install-dir", install)
	writeReleaseFixture(t, release, "1.2.4", false)
	runInstaller(t, root, tools, release, home, state, "", "--version", "v1.2.4", "--install-dir", install)

	writeReleaseFixture(t, release, "1.2.5", false)
	t.Setenv("FAKE_MV_FAIL", "1")
	output, err := installerCommand(root, tools, release, home, state, "", "--version", "v1.2.5", "--install-dir", install).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "binary installation failed") {
		t.Fatalf("error=%v output=%s", err, output)
	}
	assertInstalledVersion(t, install, "v1.2.4")
	for _, name := range packageBinaries() {
		if _, err := os.Stat(filepath.Join(install, name)); err != nil {
			t.Fatalf("restored set missing %s: %v", name, err)
		}
	}
	t.Setenv("FAKE_MV_FAIL", "")
	runInstaller(t, root, tools, release, home, state, "", "--rollback", "--install-dir", install)
	assertInstalledVersion(t, install, "v1.2.3")
}

func TestInstallerRestoresFailedUninstallAndRefusesModifiedBinary(t *testing.T) {
	root := repositoryRoot(t)
	temp := t.TempDir()
	release := filepath.Join(temp, "release")
	tools := filepath.Join(temp, "tools")
	home := filepath.Join(temp, "home")
	install := filepath.Join(temp, "bin")
	state := filepath.Join(temp, "state")
	mustMkdir(t, release, tools, home)
	writeFakeReleaseTools(t, tools)
	writeReleaseFixture(t, release, "1.2.3", false)
	runInstaller(t, root, tools, release, home, state, "", "--version", "v1.2.3", "--install-dir", install)

	t.Setenv("FAKE_MV_FAIL_UNINSTALL", "1")
	output, err := installerCommand(root, tools, release, home, state, "", "--uninstall", "--install-dir", install).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "binary uninstall failed") {
		t.Fatalf("error=%v output=%s", err, output)
	}
	for _, name := range packageBinaries() {
		if _, err := os.Stat(filepath.Join(install, name)); err != nil {
			t.Fatalf("restored set missing %s: %v", name, err)
		}
	}

	t.Setenv("FAKE_MV_FAIL_UNINSTALL", "")
	if err := os.WriteFile(filepath.Join(install, "prompt-better"), []byte("modified"), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err = installerCommand(root, tools, release, home, state, "", "--uninstall", "--install-dir", install).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "modified, or unowned") {
		t.Fatalf("error=%v output=%s", err, output)
	}
}

func TestRenderHomebrewFormulaLocksSourceBuildContract(t *testing.T) {
	root := repositoryRoot(t)
	output := filepath.Join(t.TempDir(), "prompt-better.rb")
	command := exec.Command("sh", filepath.Join(root, "scripts", "render-homebrew-formula.sh"),
		"1.2.3",
		"https://github.com/nijanthan-dev/codex-prompt-better/archive/refs/tags/v1.2.3.tar.gz",
		strings.Repeat("a", 64), strings.Repeat("b", 40), "2026-07-17T00:00:00Z", output)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render formula: %v: %s", err, data)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		`version "1.2.3"`, `depends_on arch: :arm64`, `depends_on "go" => :build`,
		"prompt-better-mcp", "prompt-better-collector", "prompt-better-admin",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("formula missing %q", required)
		}
	}
	for _, forbidden := range []string{"@VERSION@", "post_install", "xattr", "codex mcp add"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("formula contains %q", forbidden)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := findRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func packageBinaries() []string {
	return []string{"prompt-better", "prompt-better-mcp", "prompt-better-collector", "prompt-better-admin"}
}

func mustMkdir(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFakeReleaseTools(t *testing.T, directory string) {
	t.Helper()
	curl := `#!/bin/sh
set -eu
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift 2 ;;
    --*) shift ;;
    *) url=$1; shift ;;
  esac
done
cp "$FAKE_RELEASE_DIR/${url##*/}" "$output"
`
	gh := "#!/bin/sh\n[ \"${FAKE_GH_FAIL:-}\" != 1 ]\n"
	mv := `#!/bin/sh
if [ "${FAKE_MV_FAIL:-}" = 1 ]; then
  case "$1" in *prompt-better-install-*-prompt-better-collector) exit 1 ;; esac
fi
if [ "${FAKE_MV_FAIL_UNINSTALL:-}" = 1 ]; then
  case "$1" in */prompt-better-collector) exit 1 ;; esac
fi
exec /bin/mv "$@"
`
	for name, content := range map[string]string{"curl": curl, "gh": gh, "mv": mv} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func writeReleaseFixture(t *testing.T, directory, version string, tamper bool, unsafe ...string) {
	t.Helper()
	asset := fmt.Sprintf("prompt-better_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(directory, asset)
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	tarWriter := tar.NewWriter(gzipWriter)
	contents := map[string]string{
		"LICENSE": "MIT", "README.md": "synthetic", "docs/installation.md": "synthetic",
	}
	for _, name := range packageBinaries() {
		contents[name] = fmt.Sprintf("#!/bin/sh\nif [ \"${1:-}\" = --version ]; then echo \"prompt-better v%s commit=synthetic built=synthetic go=synthetic %s/%s\"; fi\n", version, runtime.GOOS, runtime.GOARCH)
	}
	for _, name := range []string{"LICENSE", "README.md", "docs/installation.md", "prompt-better", "prompt-better-admin", "prompt-better-collector", "prompt-better-mcp"} {
		content := contents[name]
		mode := int64(0o644)
		if strings.HasPrefix(name, "prompt-better") {
			mode = 0o755
		}
		header := &tar.Header{Name: name, Mode: mode, Size: int64(len(content)), ModTime: time.Unix(0, 0)}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tarWriter, content); err != nil {
			t.Fatal(err)
		}
	}
	if len(unsafe) != 0 && unsafe[0] != "" {
		header := &tar.Header{Name: unsafe[0], Mode: 0o644, Size: 1, ModTime: time.Unix(0, 0)}
		content := "x"
		if unsafe[0] == "symlink" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = "prompt-better"
			header.Size = 0
			content = ""
		}
		if unsafe[0] == "extra" {
			header.Name = "unexpected"
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tarWriter, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	digest, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(digest)
	if err := os.WriteFile(filepath.Join(directory, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", hash, asset)), 0o600); err != nil {
		t.Fatal(err)
	}
	if tamper {
		file, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("tamper"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func installerCommand(root, tools, release, home, state, ghFail string, args ...string) *exec.Cmd {
	command := exec.Command("sh", append([]string{filepath.Join(root, "scripts", "install.sh")}, args...)...)
	command.Env = append(os.Environ(),
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_RELEASE_DIR="+release, "FAKE_GH_FAIL="+ghFail, "HOME="+home, "XDG_STATE_HOME="+state)
	return command
}

func runInstaller(t *testing.T, root, tools, release, home, state, ghFail string, args ...string) {
	t.Helper()
	output, err := installerCommand(root, tools, release, home, state, ghFail, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("installer: %v: %s", err, output)
	}
}

func assertInstalledVersion(t *testing.T, directory, version string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "prompt-better"))
	if err != nil || !strings.Contains(string(data), "prompt-better "+version) {
		t.Fatalf("installed version mismatch: %v %q", err, data)
	}
}
