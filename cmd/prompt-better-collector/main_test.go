package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestRun_StatusIsSafeAndAllSourcesDisabled(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	err := run(context.Background(), []string{"--status"}, &output, func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), `"enabled":false`) != 7 || strings.Contains(output.String(), "/Users/") {
		t.Fatalf("unsafe or incomplete status: %s", output.String())
	}
}

func TestRun_RequiresExplicitConfiguration(t *testing.T) {
	t.Parallel()
	if err := run(context.Background(), nil, &bytes.Buffer{}, time.Now); err == nil {
		t.Fatal("implicit collection allowed")
	}
}

func TestValidateConfig_RejectsDuplicateRoutingIdentity(t *testing.T) {
	t.Parallel()
	base := sourceConfig{Kind: "git", SourceID: "source-one", VersionID: "version-one", CursorID: "cursor-one"}
	tests := []struct {
		name   string
		second sourceConfig
	}{
		{name: "kind", second: sourceConfig{Kind: "git", SourceID: "source-two", VersionID: "version-two", CursorID: "cursor-two"}},
		{name: "source", second: sourceConfig{Kind: "github", SourceID: "source-one", VersionID: "version-two", CursorID: "cursor-two"}},
		{name: "cursor", second: sourceConfig{Kind: "github", SourceID: "source-two", VersionID: "version-two", CursorID: "cursor-one"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateConfig(fileConfig{Owner: "owner", Sources: []sourceConfig{base, test.second}}); err == nil {
				t.Fatal("duplicate routing identity accepted")
			}
		})
	}
}

func TestConfiguredAdapter_DisabledDoesNotRead(t *testing.T) {
	t.Parallel()
	adapter, err := configuredAdapter(sourceConfig{Kind: "git", Enabled: false, Supported: true, SourceID: "source"}, strings.NewReader(""), []byte("synthetic-collector-key-32-bytes!!"))
	if err != nil || adapter == nil {
		t.Fatalf("disabled adapter: %v", err)
	}
}
