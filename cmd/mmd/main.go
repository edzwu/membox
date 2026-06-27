package main

import (
	"fmt"
	"log"

	"github.com/earendil-works/membox/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("mmd: load config: %v", err)
	}

	fmt.Printf("mmd daemon starting\n")
	fmt.Printf("config dir: %s\n", cfg.ConfigDir)
	fmt.Printf("data dir:   %s\n", cfg.DataDir)
	fmt.Println("(placeholder: real daemon loop not yet implemented)")
}
