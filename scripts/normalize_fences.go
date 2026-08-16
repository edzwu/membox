//go:build ignore

// One-shot: go run ./scripts/normalize_fences.go <notes-dir>
// Retags weak code fences (txt/c/…) to cpp when the body looks like C/C++.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"membox/internal/docrewrite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/normalize_fences.go <dir> [substr]")
		os.Exit(2)
	}
	root := os.Args[1]
	substr := "C-原生-Agent"
	if len(os.Args) >= 3 {
		substr = os.Args[2]
	}
	nFiles, nChanged := 0, 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		if substr != "" && !strings.Contains(filepath.Base(path), substr) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		in := string(b)
		out := docrewrite.NormalizeCodeFences(in)
		nFiles++
		if out == in {
			return nil
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			return nil
		}
		nChanged++
		fmt.Println("fixed", filepath.Base(path))
		return nil
	})
	fmt.Printf("scanned %d files, changed %d\n", nFiles, nChanged)
}
