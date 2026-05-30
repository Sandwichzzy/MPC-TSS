package tssnode

import (
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "launch a tss node process",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNode(cmd)
		},
	}
	cmd.Flags().BoolP("debug", "d", false, "log level,default info")
	return cmd
}

func runNode(cmd *cobra.Command) error {
	return nil
}

func PeerIDCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "parse-peer-id",
		Short: "parse peer id of the key",
		RunE: func(cmd *cobra.Command, args []string) error {

			return nil
		},
	}
	cmd.Flags().String("pub-key", "", "hex-encoded Ethereum public key with prefix")
	cmd.Flags().String("pri-key", "", "hex-encoded Ethereum private key")
	return cmd
}
