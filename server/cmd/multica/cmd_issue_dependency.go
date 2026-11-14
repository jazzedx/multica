package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// multica issue dependency {list|add|remove} — what an issue waits for. An
// issue that waits starts no run until every issue it waits for is done, and
// then starts on its own. To create an issue already waiting, use
// `multica issue create --blocked-by`.

var issueDependencyCmd = &cobra.Command{
	Use:     "dependency",
	Aliases: []string{"dep"},
	Short:   "Manage what an issue waits for",
	Long: "An issue that waits for other issues starts no agent run until all of them are done, " +
		"then starts on its own. A cancelled issue is not done: remove the dependency to let the waiting issue start.",
}

var issueDependencyListCmd = &cobra.Command{
	Use:   "list <issue-id>",
	Short: "List what an issue waits for and what waits for it",
	Args:  exactArgs(1),
	RunE:  runIssueDependencyList,
}

var issueDependencyAddCmd = &cobra.Command{
	Use:   "add <issue-id> <blocker-issue-id>",
	Short: "Make an issue wait for another issue",
	Args:  exactArgs(2),
	RunE:  runIssueDependencyAdd,
}

var issueDependencyRemoveCmd = &cobra.Command{
	Use:   "remove <issue-id> <blocker-issue-id>",
	Short: "Stop an issue waiting for another issue",
	Args:  exactArgs(2),
	RunE:  runIssueDependencyRemove,
}

func init() {
	issueDependencyCmd.AddCommand(issueDependencyListCmd)
	issueDependencyCmd.AddCommand(issueDependencyAddCmd)
	issueDependencyCmd.AddCommand(issueDependencyRemoveCmd)
	for _, c := range []*cobra.Command{issueDependencyListCmd, issueDependencyAddCmd, issueDependencyRemoveCmd} {
		c.Flags().String("output", "json", "Output format: table or json")
	}
	issueCmd.AddCommand(issueDependencyCmd)
}

func runIssueDependencyList(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve issue: %w", err)
	}
	var result map[string]any
	if err := client.GetJSON(ctx, "/api/issues/"+issueRef.ID+"/dependencies", &result); err != nil {
		return fmt.Errorf("list issue dependencies: %w", err)
	}
	return printIssueDependencies(cmd, result)
}

func runIssueDependencyAdd(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve issue: %w", err)
	}
	blockerRef, err := resolveIssueRef(ctx, client, args[1])
	if err != nil {
		return fmt.Errorf("resolve blocker issue: %w", err)
	}
	var result map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+issueRef.ID+"/dependencies", map[string]any{"blocked_by": blockerRef.ID}, &result); err != nil {
		return fmt.Errorf("add issue dependency: %w", err)
	}
	return printIssueDependencies(cmd, result)
}

func runIssueDependencyRemove(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve issue: %w", err)
	}
	blockerRef, err := resolveIssueRef(ctx, client, args[1])
	if err != nil {
		return fmt.Errorf("resolve blocker issue: %w", err)
	}
	var result map[string]any
	if err := client.DeleteJSONResponse(ctx, "/api/issues/"+issueRef.ID+"/dependencies/"+blockerRef.ID, &result); err != nil {
		return fmt.Errorf("remove issue dependency: %w", err)
	}
	return printIssueDependencies(cmd, result)
}

func printIssueDependencies(cmd *cobra.Command, result map[string]any) error {
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	printIssueDependencyRows("WAITS FOR", result["blocked_by"])
	printIssueDependencyRows("BLOCKS", result["blocks"])
	return nil
}

func printIssueDependencyRows(heading string, raw any) {
	rows, _ := raw.([]any)
	if len(rows) == 0 {
		fmt.Fprintf(os.Stdout, "%s: none\n", heading)
		return
	}
	fmt.Fprintf(os.Stdout, "%s:\n", heading)
	for _, r := range rows {
		ref, ok := r.(map[string]any)
		if !ok {
			continue
		}
		fmt.Fprintf(os.Stdout, "  %v  %v  %v\n", ref["identifier"], ref["status"], ref["title"])
	}
}
