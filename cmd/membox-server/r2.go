package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// newR2Command exposes direct object-store CRUD for ops and the M1 smoke test
// (scripts/remote-smoke.sh). Production document traffic goes through the HTTP
// API, never through these commands.
func newR2Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "r2",
		Short: "Direct Cloudflare R2 object operations (ops/smoke)",
	}
	put := &cobra.Command{
		Use:   "put <file>",
		Short: "Upload a file; default key is sha256/<file hash>",
		Args:  cobra.ExactArgs(1),
		RunE:  runR2Put,
	}
	put.Flags().String("key", "", "override the object key")
	get := &cobra.Command{
		Use:   "get <key>",
		Short: "Download an object and report its SHA-256",
		Args:  cobra.ExactArgs(1),
		RunE:  runR2Get,
	}
	get.Flags().StringP("out", "o", "", "write to file instead of stdout")
	cmd.AddCommand(put, get,
		&cobra.Command{
			Use:   "head <key>",
			Short: "Show object size and existence",
			Args:  cobra.ExactArgs(1),
			RunE:  runR2Head,
		},
		&cobra.Command{
			Use:   "rm <key>",
			Short: "Delete an object",
			Args:  cobra.ExactArgs(1),
			RunE:  runR2Delete,
		},
		&cobra.Command{
			Use:   "ls [prefix]",
			Short: "List objects under a prefix (smoke/ops only, never production listings)",
			Args:  cobra.MaximumNArgs(1),
			RunE:  runR2List,
		},
	)
	return cmd
}

func runR2Put(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := openR2(cfg)
	if err != nil {
		return err
	}

	file, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash %s: %w", args[0], err)
	}
	sum := hex.EncodeToString(hash.Sum(nil))

	key, _ := cmd.Flags().GetString("key")
	if key == "" {
		key = "sha256/" + sum
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := store.Put(cmd.Context(), key, file, info.Size()); err != nil {
		return err
	}
	fmt.Printf("put ok\n  key:    %s\n  sha256: %s\n  size:   %d\n", key, sum, info.Size())
	return nil
}

func runR2Get(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := openR2(cfg)
	if err != nil {
		return err
	}

	body, err := store.Get(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	defer body.Close()

	outPath, _ := cmd.Flags().GetString("out")
	var sink io.Writer = os.Stdout
	if outPath != "" {
		out, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer out.Close()
		sink = out
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(sink, hash), body)
	if err != nil {
		return err
	}
	if outPath != "" {
		fmt.Printf("get ok\n  key:    %s\n  sha256: %s\n  size:   %d\n", args[0], hex.EncodeToString(hash.Sum(nil)), size)
	}
	return nil
}

func runR2Head(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := openR2(cfg)
	if err != nil {
		return err
	}
	object, exists, err := store.Head(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("%s: not found\n", args[0])
		return nil
	}
	fmt.Printf("%s: %d bytes\n", object.Key, object.Size)
	return nil
}

func runR2Delete(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := openR2(cfg)
	if err != nil {
		return err
	}
	if err := store.Delete(cmd.Context(), args[0]); err != nil {
		return err
	}
	fmt.Printf("deleted %s\n", args[0])
	return nil
}

func runR2List(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	store, err := openR2(cfg)
	if err != nil {
		return err
	}
	prefix := ""
	if len(args) == 1 {
		prefix = args[0]
	}
	objects, err := store.List(cmd.Context(), prefix, 100)
	if err != nil {
		return err
	}
	for _, object := range objects {
		fmt.Printf("%12d  %s\n", object.Size, object.Key)
	}
	fmt.Printf("%d object(s)\n", len(objects))
	return nil
}
