package api

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Serve starts the Python FastAPI service as a subprocess.
// This keeps the existing service/routes.py investment alive while
// the Go CLI becomes the unified entrypoint.
func Serve(port int) error {
	cmd := exec.Command(
		"python", "-m", "uvicorn", "service.app:app",
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--reload",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = findRepoRoot()

	log.Printf("[api] starting uvicorn on port %d", port)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start uvicorn: %w", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}()

	return cmd.Wait()
}

func findRepoRoot() string {
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return "."
}
