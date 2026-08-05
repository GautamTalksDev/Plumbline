//go:build !linux

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newCrawlCmd(jsonOut *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "crawl",
		Short: "Operator: sandboxed crawl (Linux only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("plumb crawl is Linux-only (this OS is %s); see docs/CRAWLING.md", runtime.GOOS)
		},
	}
}
