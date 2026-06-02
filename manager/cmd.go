package manager

import (
	"github.com/Sandwichzzy/tss/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manager",
		Short: "launch a tss manager process",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd)
		},
	}
	return cmd
}

func run(cmd *cobra.Command) error {
	config := config.GetConfigFromCmd(cmd)

	// Manager 先初始化本地持久化存储，用来保存任务和中间状态。
	managerStore, err := store.NewStorage(config.Manager.DBDir)
	if err != nil {
		return err
	}
}
