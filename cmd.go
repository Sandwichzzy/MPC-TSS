package main

import (
	"context"
	"os"

	"github.com/Sandwichzzy/tss/config"
	"github.com/spf13/cobra"

	"github.com/ethereum/go-ethereum/log"

	"github.com/Sandwichzzy/tss/manager"
	"github.com/Sandwichzzy/tss/node/cmd/tssnode"
)

func main() {
	log.SetDefault(
		log.NewLogger(
			log.NewTerminalHandlerWithLevel(os.Stdout, log.LevelInfo, true),
		),
	)
	// 根命令只负责挂载全局配置，并把配置对象提前放进 context，供子命令直接读取。
	rootCmd := &cobra.Command{
		Use:   "tss",
		Short: "Tss Start Daemon",
		//PersistentPreRunE 所有子命令都会继承执行,加载配置
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfgFile, _ := cmd.Flags().GetString("config")
			loadedCfg, err := config.LoadConfig(cfgFile)
			if err == nil {
				// 成功加载后，把配置写回命令上下文，后续 manager/node 都从这里取。
				return config.SetCmdConfig(cmd, loadedCfg)
			} else {
				return nil
			}
		},
	}

	// 三个子命令分别对应 manager、node 和从密钥推导 peer id 的辅助工具。
	rootCmd.AddCommand(
		manager.Command(),
		tssnode.Command(),
		tssnode.PeerIDCommand(),
	)

	rootCmd.PersistentFlags().StringP("config", "c", "config", "configuration file with extension")

	ctx := context.Background()
	// 先放一个可写的配置对象，PersistentPreRunE 会把解析结果填进去。
	ctx = context.WithValue(ctx, "config", &config.Configuration{})
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
