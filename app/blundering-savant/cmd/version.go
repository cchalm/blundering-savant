package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version information set by main package
var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

// SetVersionInfo sets the version information from build-time variables
func SetVersionInfo(v, commit, buildT string) {
	version = v
	gitCommit = commit
	buildTime = buildT
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Blundering Savant Bot %s\n", version)
		fmt.Printf("Git Commit: %s\n", gitCommit)
		fmt.Printf("Build Time: %s\n", buildTime)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
