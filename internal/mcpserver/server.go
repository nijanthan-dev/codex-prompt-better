// Package mcpserver owns the bounded local MCP transport lifecycle.
package mcpserver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ErrBusy            = errors.New("server busy")
	ErrMessageTooLarge = errors.New("protocol message too large")
	ErrProtocol        = errors.New("protocol failure")
)

const (
	DefaultMaxInputBytes = 64 * 1024
	DefaultMaxConcurrent = 4
	DefaultTimeout       = 2 * time.Second
)

type Options struct {
	MaxInputBytes int
	MaxConcurrent int
	Timeout       time.Duration
	Logger        *slog.Logger
}

type Server struct {
	sdk     *mcp.Server
	limit   chan struct{}
	timeout time.Duration
	logger  *slog.Logger
}

func New(options Options) (*Server, error) {
	if options.MaxInputBytes <= 0 || options.MaxConcurrent <= 0 || options.Timeout <= 0 || options.Logger == nil {
		return nil, errors.New("invalid MCP server options")
	}
	return &Server{
		sdk:   mcp.NewServer(&mcp.Implementation{Name: "prompt-better", Version: "0.2.0-dev"}, nil),
		limit: make(chan struct{}, options.MaxConcurrent), timeout: options.Timeout, logger: options.Logger,
	}, nil
}

func DefaultOptions(logger *slog.Logger) Options {
	return Options{
		MaxInputBytes: DefaultMaxInputBytes,
		MaxConcurrent: DefaultMaxConcurrent,
		Timeout:       DefaultTimeout,
		Logger:        logger,
	}
}

func (server *Server) SDK() *mcp.Server { return server.sdk }

func (server *Server) Acquire(ctx context.Context) (context.Context, func(), error) {
	select {
	case server.limit <- struct{}{}:
	default:
		return nil, nil, ErrBusy
	}
	bounded, cancel := context.WithTimeout(ctx, server.timeout)
	release := func() {
		cancel()
		<-server.limit
	}
	return bounded, release, nil
}

func (server *Server) Run(ctx context.Context, transport mcp.Transport) error {
	if err := server.sdk.Run(ctx, transport); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		server.logger.Warn("mcp lifecycle ended", "code", "protocol_failure")
		return ErrProtocol
	}
	return nil
}

func NewStdioTransport(reader io.Reader, writer io.Writer, maxInputBytes int) (mcp.Transport, error) {
	if reader == nil || writer == nil || maxInputBytes <= 0 {
		return nil, errors.New("invalid stdio transport")
	}
	var readerCloser io.Closer
	if closer, ok := reader.(io.Closer); ok {
		readerCloser = closer
	}
	var writerCloser io.Closer
	if closer, ok := writer.(io.Closer); ok {
		writerCloser = closer
	}
	boundedReader := &boundedReadCloser{reader: bufio.NewReader(reader), closer: readerCloser, max: maxInputBytes}
	boundedWriter := &streamWriteCloser{Writer: writer, closer: writerCloser}
	return &stdioTransport{
		delegate: &mcp.IOTransport{Reader: boundedReader, Writer: boundedWriter},
		reader:   boundedReader,
		writer:   boundedWriter,
	}, nil
}

type stdioTransport struct {
	delegate mcp.Transport
	reader   io.Closer
	writer   io.Closer
}

func (transport *stdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := transport.delegate.Connect(ctx)
	if err != nil {
		return nil, err
	}
	wrapped := &contextConnection{
		Connection: connection,
		reader:     transport.reader,
		writer:     transport.writer,
		done:       make(chan struct{}),
	}
	go func() {
		select {
		case <-ctx.Done():
			wrapped.closeStreams()
		case <-wrapped.done:
		}
	}()
	return wrapped, nil
}

type contextConnection struct {
	mcp.Connection
	reader io.Closer
	writer io.Closer
	once   sync.Once
	done   chan struct{}
}

func (connection *contextConnection) Close() error {
	connection.closeStreams()
	return connection.Connection.Close()
}

func (connection *contextConnection) closeStreams() {
	connection.once.Do(func() {
		close(connection.done)
		_ = connection.reader.Close()
		_ = connection.writer.Close()
	})
}

type boundedReadCloser struct {
	reader *bufio.Reader
	closer io.Closer
	once   sync.Once
	max    int
	buffer []byte
	end    error
}

func (reader *boundedReadCloser) Read(target []byte) (int, error) {
	if len(reader.buffer) == 0 {
		if reader.end != nil {
			err := reader.end
			reader.end = nil
			return 0, err
		}
		line, err := reader.readLine()
		reader.buffer = line
		if err != nil && len(line) == 0 {
			return 0, err
		}
		reader.end = err
	}
	count := copy(target, reader.buffer)
	reader.buffer = reader.buffer[count:]
	return count, nil
}

func (reader *boundedReadCloser) readLine() ([]byte, error) {
	line := make([]byte, 0, min(reader.max, 4096))
	for {
		chunk, err := reader.reader.ReadSlice('\n')
		if len(line)+len(chunk) > reader.max {
			return nil, ErrMessageTooLarge
		}
		line = append(line, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func (reader *boundedReadCloser) Close() error {
	var err error
	reader.once.Do(func() {
		if reader.closer != nil {
			err = reader.closer.Close()
		}
	})
	return err
}

type streamWriteCloser struct {
	io.Writer
	closer io.Closer
	once   sync.Once
}

func (writer *streamWriteCloser) Close() error {
	var err error
	writer.once.Do(func() {
		if writer.closer != nil {
			err = writer.closer.Close()
		}
	})
	return err
}
