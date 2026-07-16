package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type spikeRequest struct {
	Value string `json:"value"`
}

func TestSDKLifecycleSpike_LegacyProtocolNegotiation(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"2024-11-05", "2025-03-26"} {
		t.Run(version, func(t *testing.T) {
			server, err := New(Options{
				MaxInputBytes: 4096, MaxConcurrent: 1, Timeout: time.Second,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			inputReader, inputWriter := io.Pipe()
			outputReader, outputWriter := io.Pipe()
			transport, err := NewStdioTransport(inputReader, outputWriter, 4096)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			exited := make(chan error, 1)
			go func() { exited <- server.Run(ctx, transport) }()
			if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+version+`","capabilities":{},"clientInfo":{"name":"legacy","version":"1.0"}}}`+"\n"); err != nil {
				t.Fatal(err)
			}
			var response struct {
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			line, err := bufio.NewReader(outputReader).ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(line, &response); err != nil {
				t.Fatalf("response=%q err=%v", line, err)
			}
			if response.Result.ProtocolVersion != version {
				t.Fatalf("protocol=%s", response.Result.ProtocolVersion)
			}
			if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`+"\n"); err != nil {
				t.Fatal(err)
			}
			cancel()
			_ = inputWriter.Close()
			select {
			case err := <-exited:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("legacy session did not exit")
			}
		})
	}
}

type spikeResult struct {
	Value string `json:"value"`
}

func TestSDKLifecycleSpike_InitializeDiscoverCallDisconnect(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := mcp.NewServer(&mcp.Implementation{Name: "prompt-better-spike", Version: "0.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Synthetic lifecycle probe."},
		func(_ context.Context, _ *mcp.CallToolRequest, input spikeRequest) (*mcp.CallToolResult, spikeResult, error) {
			return &mcp.CallToolResult{}, spikeResult{Value: input.Value}, nil
		})

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	exited := make(chan error, 1)
	go func() { exited <- server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "synthetic-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if session.InitializeResult() == nil || session.InitializeResult().Capabilities.Tools == nil {
		t.Fatal("tool capability not negotiated")
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tool discovery: %#v, %v", listed, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"value": "synthetic"}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("unexpected tool result: %#v, %v", result, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-exited:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("server exit: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not exit promptly")
	}
}

func TestSDKLifecycleSpike_PropagatesCancellation(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "prompt-better-spike", Version: "0.0.0"}, nil)
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	mcp.AddTool(server, &mcp.Tool{Name: "slow", Description: "Synthetic cancellation probe."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			started <- struct{}{}
			<-ctx.Done()
			canceled <- struct{}{}
			return &mcp.CallToolResult{}, map[string]any{}, nil
		})

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "synthetic-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	callContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := session.CallTool(callContext, &mcp.CallToolParams{Name: "slow", Arguments: map[string]any{}})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("call error=%v, want cancellation", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("handler did not observe cancellation")
	}
}
