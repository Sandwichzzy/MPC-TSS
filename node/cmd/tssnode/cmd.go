package tssnode

import (
	"errors"
	"strconv"

	"github.com/Sandwichzzy/tss/config"
	"github.com/Sandwichzzy/tss/node/store"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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
	cmd.Flags().Bool("non-prod", false, "enable non-production node HTTP behavior")
	cmd.Flags().Bool("full", false, "wait for full peer connectivity before starting TSS")
	return cmd
}

func runNode(cmd *cobra.Command) error {
	nonProd, _ := cmd.Flags().GetBool("non-prod")
	waitPeersFullConnected, _ := cmd.Flags().GetBool("full")
	debug, _ := cmd.Flags().GetBool("debug")

	// 先确定日志级别，方便后续启动阶段排查问题。
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	// Node 的运行参数统一从全局配置里读取。
	cfg := config.GetConfigFromCmd(cmd)
	if len(cfg.Node.PrivateKey) == 0 {
		return errors.New("need to config private key")
	}

	// 私钥用于生成本地节点身份和后续签名能力。
	privKey, err := crypto.HexToECDSA(cfg.Node.PrivateKey)
	if err != nil {
		return err
	}
	// 本地 LevelDB 用于缓存节点状态、任务进度和协议中间结果。
	store, err := store.NewStorage(cfg.Node.DBDir)
	if err != nil {
		return err
	}

	// P2P 端口从字符串配置转换为整数，供 tsslib 建连使用。
	p2pPort, err := strconv.Atoi(cfg.Node.P2PPort)
	if err != nil {
		log.Error().Err(err).Msg("p2p port value in config file, can not convert to int type")
		return err
	}

	// tssInstance 是 Node 的核心：负责 P2P 协议、keygen 和 sign 流程。
	//tssInstance, err := tsslib.NewTss(
	//	cfg.Node.BootstrapPeers,
	//	waitPeersFullConnected,
	//	p2pPort,
	//	privKey,
	//	cfg.Node.BaseDir,
	//	common.TssConfig{
	//		PreParamTimeout: cfg.Node.PreParamTimeout,
	//		KeyGenTimeout:   cfg.Node.KeyGenTimeout,
	//		KeySignTimeout:  cfg.Node.KeySignTimeout,
	//		EnableMonitor:   false,
	//	},
	//	cfg.Node.PreParamFile,
	//	cfg.Node.ExternalIP,
	//	cfg.Node.Secrets.Enable,
	//	cfg.Node.Secrets.SecretId,
	//	cfg.Node.Shamir,
	//	store,
	//)

	//if err != nil {
	//	log.Fatal().Err(err).Msg("fail to create tss server instance")
	//	return err
	//}
	//if err := tssInstance.Start(); err != nil {
	//	log.Error().Err(err).Msg("fail to start tss server")
	//	return err
	//}
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
