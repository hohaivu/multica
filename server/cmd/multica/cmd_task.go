package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Report on the current agent task's run outcome",
}

// ── Blocked ─────────────────────────────────────────────────────────────────

var taskBlockedCmd = &cobra.Command{
	Use:   "blocked",
	Short: "Report this run as blocked, not completed",
	Long: `Record the current task's run as failed with failure_reason=agent_blocked,
instead of the daemon reporting it as completed.

This is for a genuine environment/permission blocker you cannot resolve
yourself — e.g. the checkout is unreadable even after confirming the path
against 'multica repo checkout' or 'git worktree list'. Call this before your
final handoff comment so the run is not recorded as a success (VUH-140).`,
	Args: cobra.NoArgs,
	RunE: runTaskBlocked,
}

func runTaskBlocked(cmd *cobra.Command, _ []string) error {
	reason, _ := cmd.Flags().GetString("reason")
	if reason == "" {
		return fmt.Errorf("--reason is required")
	}
	if !inAgentExecutionContext() {
		return fmt.Errorf("multica task blocked requires an agent task context (MULTICA_AGENT_ID/MULTICA_TASK_ID); it reports the current daemon task, not an arbitrary one")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var result map[string]any
	if err := client.PostJSON(ctx, "/api/tasks/blocked", map[string]any{"reason": reason}, &result); err != nil {
		return fmt.Errorf("report task blocked: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Task recorded as blocked: %s\n", reason)

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	return nil
}

func init() {
	taskBlockedCmd.Flags().String("reason", "", "What is blocking, and who must act (required)")
	taskBlockedCmd.Flags().String("output", "table", "Output format: table or json")

	taskCmd.AddCommand(taskBlockedCmd)
}
