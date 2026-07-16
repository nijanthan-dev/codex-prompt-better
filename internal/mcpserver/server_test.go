package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
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

func TestServer_RunCancellationClosesBlockedStreams(t *testing.T) {
	t.Parallel()
	reader := newBlockingStream()
	writer := newBlockingStream()
	transport, err := NewStdioTransport(reader, writer, 1024)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{MaxInputBytes: 1024, MaxConcurrent: 1, Timeout: time.Second, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan error, 1)
	go func() { exited <- server.Run(ctx, transport) }()
	cancel()
	select {
	case <-reader.closed:
	case <-time.After(time.Second):
		t.Fatal("reader not closed")
	}
	select {
	case <-writer.closed:
	case <-time.After(time.Second):
		t.Fatal("writer not closed")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("server did not exit")
	}
}

func TestServer_RunCancellationUnblocksBlockedConsumer(t *testing.T) {
	t.Parallel()
	reader, input := io.Pipe()
	defer input.Close()
	writer := &blockedWriter{started: make(chan struct{}), closed: make(chan struct{})}
	transport, err := NewStdioTransport(reader, writer, 4096)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{MaxInputBytes: 4096, MaxConcurrent: 1, Timeout: time.Second, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan error, 1)
	go func() { exited <- server.Run(ctx, transport) }()
	if _, err := io.WriteString(input, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"blocked","version":"1.0"}}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("consumer did not block")
	}
	cancel()
	select {
	case <-writer.closed:
	case <-time.After(time.Second):
		t.Fatal("blocked consumer not closed")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("server did not exit")
	}
}

type blockedWriter struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (writer *blockedWriter) Write([]byte) (int, error) {
	writer.once.Do(func() { close(writer.started) })
	<-writer.closed
	return 0, io.ErrClosedPipe
}

func (writer *blockedWriter) Close() error {
	select {
	case <-writer.closed:
	default:
		close(writer.closed)
	}
	return nil
}

type blockingStream struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingStream() *blockingStream {
	return &blockingStream{closed: make(chan struct{})}
}

func (stream *blockingStream) Read([]byte) (int, error) {
	<-stream.closed
	return 0, io.EOF
}

func (stream *blockingStream) Write(data []byte) (int, error) {
	select {
	case <-stream.closed:
		return 0, io.ErrClosedPipe
	default:
		return len(data), nil
	}
}

func (stream *blockingStream) Close() error {
	stream.once.Do(func() { close(stream.closed) })
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
