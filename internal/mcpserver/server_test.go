package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestServer_AcquireBoundsConcurrencyAndTime(t *testing.T) {
	t.Parallel()
	server, err := New(Options{MaxInputBytes: 32, MaxConcurrent: 1, Timeout: 10 * time.Millisecond, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, release, err := server.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.Acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquire=%v, want busy", err)
	}
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatal(ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("handler timeout not enforced")
	}
	release()
}

func TestStdioTransport_RejectsOversizedMessageWithoutOutput(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	transport, err := NewStdioTransport(strings.NewReader(strings.Repeat("x", 33)+"\n"), &output, 32)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Read(context.Background()); err == nil {
		t.Fatal("oversized message accepted")
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected stdout bytes: %q", output.String())
	}
}

func TestBoundedReader_PreservesFragmentedMessages(t *testing.T) {
	t.Parallel()
	source := "{\"jsonrpc\":\"2.0\",\"id\":1}\n{\"jsonrpc\":\"2.0\",\"id\":2}\n"
	reader := &boundedReadCloser{reader: bufio.NewReader(&fragmentReader{data: []byte(source)}), max: 128}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != source {
		t.Fatalf("fragmented data=%q err=%v", data, err)
	}
}

type fragmentReader struct{ data []byte }

func (reader *fragmentReader) Read(target []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	size := min(3, len(reader.data), len(target))
	copy(target, reader.data[:size])
	reader.data = reader.data[size:]
	return size, nil
}

func TestServer_RunSanitizesProtocolFailure(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	server, err := New(Options{MaxInputBytes: 64, MaxConcurrent: 1, Timeout: time.Second, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewStdioTransport(strings.NewReader("not-json\n"), io.Discard, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Run(context.Background(), transport); !errors.Is(err, ErrProtocol) {
		t.Fatalf("run error=%v", err)
	}
	if got := logs.String(); !strings.Contains(got, "protocol_failure") || strings.Contains(got, "not-json") {
		t.Fatalf("unsafe diagnostics: %q", got)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
