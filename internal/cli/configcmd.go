package cli

import (
	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Work with fugaro.yaml"}
	cmd.AddCommand(&cobra.Command{
		Use:   "example",
		Short: "Print an annotated fugaro.yaml template",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(config.ExampleFor(selectedProjectName(cmd.Context())))
			return err
		},
	})
	return cmd
}
