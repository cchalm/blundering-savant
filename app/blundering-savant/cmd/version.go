package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Blundering Savant Bot v1.0.0") // TODO version number
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
