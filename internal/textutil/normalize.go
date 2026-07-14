package textutil

import (
	"strings"
	"unicode/utf8"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func Normalize(input string) (string, error) {
	if !utf8.ValidString(input) {
		return "", contracts.NewError(contracts.ErrorCodeInvalidSchema, "input must be valid UTF-8", "input", false)
	}
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	lines := strings.Split(input, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if blank || len(out) == 0 {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	value := strings.TrimSpace(strings.Join(out, "\n"))
	if value == "" {
		return "", contracts.NewError(contracts.ErrorCodeInvalidSchema, "input must not be empty", "input", false)
	}
	return value, nil
}
