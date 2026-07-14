package main

import (
	"context"
	"os"

	"github.com/nijanthan-dev/codex-prompt-better/internal/cli"
)

func main() {
	streams := cli.Streams{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr}
	os.Exit(cli.Run(context.Background(), os.Args[1:], streams))
}
