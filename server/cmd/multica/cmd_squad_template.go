package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/squadtemplate"
)

var squadTemplateCmd = &cobra.Command{
	Use:   "template",
	Short: "Work with a squad template directory",
}

var squadTemplateCheckCmd = &cobra.Command{
	Use:   "check <dir>",
	Short: "Check that a server can offer the squad templates in a directory",
	Long: "Reads the directory the way the server does (manifest.json and the template\n" +
		"tree it names) and lists what it found. Fails when the directory cannot be\n" +
		"loaded, or when a template would misbehave once installed.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		catalog, err := squadtemplate.LoadDir(args[0])
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		for _, tpl := range catalog.Templates {
			fmt.Fprintf(out, "%s: %d agents, %d skills, led by %s\n", tpl.Key, len(tpl.Agents), len(tpl.Skills), tpl.Leader)
		}
		problems := squadtemplate.Lint(catalog)
		for _, problem := range problems {
			fmt.Fprintln(out, "problem:", problem)
		}
		if len(problems) > 0 {
			return fmt.Errorf("%d problem(s) in %s", len(problems), args[0])
		}
		if len(catalog.Templates) == 0 {
			return fmt.Errorf("%s lists no templates", args[0])
		}
		return nil
	},
}

func init() {
	squadTemplateCmd.AddCommand(squadTemplateCheckCmd)
	squadCmd.AddCommand(squadTemplateCmd)
}
