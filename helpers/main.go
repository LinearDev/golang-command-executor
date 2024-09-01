package helpers

import (
	"strings"
)

func UTF16BytesToString(b []byte) (string, error) {
	return string(b), nil
}

func EscapeANSICodes(input string) string {
	input = strings.ReplaceAll(input, "\x1b", "\\x1b")
	input = strings.ReplaceAll(input, "\u001b", "\\u001b")
	input = strings.ReplaceAll(input, "\033", "\\033")
	return input
}
