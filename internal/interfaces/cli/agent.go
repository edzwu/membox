package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newAgentCommand exposes status/doctor against the running Web Companion.
// It never starts a second Agent manager.
func newAgentCommand(runtime *runtime) *cobra.Command {
	command := parentCommand("agent", "Inspect the Pi Agent control plane", "agent requires a subcommand")
	command.AddCommand(
		newAgentStatusCommand(runtime),
		newAgentDoctorCommand(runtime),
	)
	return command
}

func newAgentStatusCommand(runtime *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Agent availability via the Web Companion",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			box, err := runtime.get()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			status, err := box.WebStatus(ctx)
			if err != nil {
				return err
			}
			if !status.Running {
				fmt.Fprintln(cmd.OutOrStdout(), "companion: stopped")
				fmt.Fprintln(cmd.OutOrStdout(), "agent: unavailable (start the Web Companion first)")
				return nil
			}
			_, token := box.BridgeInfo()
			body, code, err := companionAgentGET(ctx, status.URL, token, "/api/agent/status")
			if err != nil {
				return err
			}
			if code >= 400 {
				return fmt.Errorf("agent status HTTP %d: %s", code, truncateOut(body, 200))
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "companion: running (%s)\n", status.URL)
			fmt.Fprintf(cmd.OutOrStdout(), "agent enabled: %v\n", payload["enabled"])
			fmt.Fprintf(cmd.OutOrStdout(), "agent available: %v\n", payload["available"])
			fmt.Fprintf(cmd.OutOrStdout(), "agent state: %v\n", payload["state"])
			if v := payload["pi_version"]; v != nil && v != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "pi version: %v\n", v)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "workers: %v / %v\n", payload["loaded_workers"], payload["max_workers"])
			if payload["error"] != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "error: %v\n", payload["error"])
			}
			return nil
		},
	}
}

func newAgentDoctorCommand(runtime *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose Pi Agent integration via the Web Companion",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			box, err := runtime.get()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			status, err := box.WebStatus(ctx)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !status.Running {
				fmt.Fprintln(out, "companion_reachable: false")
				fmt.Fprintln(out, "hint: run `mm web start` or open the TUI so the companion can host Agent workers")
				return nil
			}
			// Doctor uses status + models as a lightweight probe. Full manager.Doctor
			// is process-local; remote CLI approximates via public endpoints.
			_, token := box.BridgeInfo()
			body, code, err := companionAgentGET(ctx, status.URL, token, "/api/agent/status")
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "companion_reachable: true\n")
			fmt.Fprintf(out, "companion_url: %s\n", status.URL)
			if code >= 400 {
				fmt.Fprintf(out, "agent_status_http: %d\n", code)
				fmt.Fprintf(out, "body: %s\n", truncateOut(body, 300))
				return nil
			}
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			fmt.Fprintf(out, "enabled: %v\n", payload["enabled"])
			fmt.Fprintf(out, "available: %v\n", payload["available"])
			fmt.Fprintf(out, "state: %v\n", payload["state"])
			fmt.Fprintf(out, "pi_version: %v\n", payload["pi_version"])
			fmt.Fprintf(out, "write_tools: %v\n", payload["write_tools"])
			if payload["error"] != nil {
				fmt.Fprintf(out, "error: %v\n", payload["error"])
			}
			if payload["available"] == true {
				modelsBody, modelsCode, modelsErr := companionAgentGET(ctx, status.URL, token, "/api/agent/models")
				if modelsErr != nil {
					fmt.Fprintf(out, "models: error %v\n", modelsErr)
				} else if modelsCode >= 400 {
					fmt.Fprintf(out, "models: HTTP %d %s\n", modelsCode, truncateOut(modelsBody, 200))
					fmt.Fprintln(out, "hint: authenticate providers with the pi CLI if models are missing")
				} else {
					var models map[string]any
					_ = json.Unmarshal(modelsBody, &models)
					list, _ := models["models"].([]any)
					fmt.Fprintf(out, "models_available: %d\n", len(list))
				}
			} else {
				fmt.Fprintln(out, "hint: ensure `pi` is on PATH or set agent.pi_path; run `pi --version`")
			}
			return nil
		},
	}
}

func companionAgentGET(ctx context.Context, baseURL, token, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return nil, 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}

func truncateOut(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
