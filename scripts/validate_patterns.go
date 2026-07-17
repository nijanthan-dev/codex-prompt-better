//go:build ignore

// Command validate-patterns enforces repository-wide review invariants.
package main

import (
	"fmt"
	"os"

	"github.com/nijanthan-dev/codex-prompt-better/internal/reviewpatterns"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	failures, err := reviewpatterns.ValidateTree(root)
	if err != nil {
		fail(err)
	}
	if len(failures) != 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(1)
	}
	fmt.Println("PASS review patterns")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
