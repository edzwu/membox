package cli

import (
	"fmt"
	"io"
	"os"
)

const (
	createDateFmt = "2006-01-02"
	updateTimeFmt = "2006-01-02-15:04:05"
)

func shortUUID(uuid string) string {
	if len(uuid) <= 5 {
		return uuid
	}
	return uuid[:5]
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func readContentOrStdin(arg string) (string, error) {
	if arg != "" {
		return arg, nil
	}
	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("stat stdin: %w", err)
	}
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	}
	return "", nil
}
