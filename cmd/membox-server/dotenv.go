package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadDotEnv applies KEY=VALUE lines from path to the process environment
// without overwriting variables that are already set (real env wins). A
// missing file is not an error: configuration comes from the environment
// first and .env is only a development convenience (ADR-004).
func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s:%d: want KEY=VALUE, got %q", path, line, text)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("%s:%d: %w", path, line, err)
			}
		}
	}
	return scanner.Err()
}
