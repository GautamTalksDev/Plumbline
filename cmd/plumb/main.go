// Command plumb is the Plumbline CLI entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "plumb",
		Short: "Plumbline — MCP tool-metadata integrity observer",
		Long:  "Plumbline observes post-publication changes in MCP server tool metadata.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
