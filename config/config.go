package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type Configuration struct {
	Manager           ManagerConfig `json:"manager"`
	Node              NodeConfig    `json:"node"`
	TimedTaskInterval string        `json:"timed_task_interval" mapstructure:"timed_task_interval"` //周期任务间隔，代码里会用 time.ParseDuration 解析，比如 10s。
}

type ManagerConfig struct {
	WsAddr            string   `json:"ws_addr" mapstructure:"ws_addr"`                         //Manager 监听的 WebSocket 地址，Node 会连到这里。
	Nodes             []string `json:"nodes" mapstructure:"nodes"`                             //期望参与的 Node 公钥列表，不是网络地址。
	HttpAddr          string   `json:"http_addr" mapstructure:"http_addr"`                     //Manager 对外 HTTP API 地址。
	JwtSecret         string   `json:"jwt_secret" mapstructure:"jwt_secret"`                   //HTTP 鉴权密钥，非空时会包一层 JWT 中间件。代码要求它是 32 字节 hex。
	DBDir             string   `json:"db_dir" mapstructure:"db_dir"`                           //Manager 的 LevelDB 存储目录，保存 CPK 和消息状态。
	PrivateKey        string   `json:"private_key" mapstructure:"private_key"`                 //历史预留字段。
	KeygenTimeout     string   `json:"keygen_timeout" mapstructure:"keygen_timeout"`           //Manager 等待 keygen 响应的超时时间。
	CPKConfirmTimeout string   `json:"cpk_confirm_timeout" mapstructure:"cpk_confirm_timeout"` //CPK 确认超时
	AskTimeout        string   `json:"ask_timeout" mapstructure:"ask_timeout"`                 //签名前“是否愿意签”的询问超时。
	SignTimeout       string   `json:"sign_timeout" mapstructure:"sign_timeout"`               //正式签名阶段的等待超时。
}

type NodeConfig struct {
	BaseDir         string               `json:"base_dir" mapstructure:"base_dir"`                   //Node 的基础工作目录，给 tsslib 存本地状态和预参数用。
	DBDir           string               `json:"db_dir" mapstructure:"db_dir"`                       //Node 自己的 LevelDB 目录，存 message hash、成员状态等。
	WsAddr          string               `json:"ws_addr" mapstructure:"ws_addr"`                     //Node 要连接的 Manager WebSocket 地址
	HttpAddr        string               `json:"http_addr" mapstructure:"http_addr"`                 //Node 对外 HTTP 服务地址。
	JwtSecret       string               `json:"jwt_secret" mapstructure:"jwt_secret"`               //Node HTTP 服务鉴权密钥。
	L2EthRpc        string               `json:"l2_eth_rpc" mapstructure:"l2_eth_rpc"`               //L2 链 RPC 地址，当前主流程里没看到直接使用。
	DisableHTTP2    bool                 `json:"disable_http2" mapstructure:"disable_http2"`         //是否禁用 HTTP/2
	PrivateKey      string               `json:"private_key" mapstructure:"private_key"`             //Node 的 secp256k1 私钥，必须填；用于派生 peer ID、连接认证和签名相关流程。
	PreParamFile    string               `json:"pre_param_file" mapstructure:"pre_param_file"`       //预先生成的 TSS pre-parameters 文件路径；为空时会尝试本地状态或重新生成。
	P2PPort         string               `json:"p2p_port" mapstructure:"p2p_port"`                   //Node 的 libp2p 监听端口。
	BootstrapPeers  string               `json:"bootstrap_peers" mapstructure:"bootstrap_peers"`     //引导节点 multiaddr 列表，逗号分隔。
	ExternalIP      string               `json:"external_ip" mapstructure:"external_ip"`             //Node 对外广播的外网 IP。
	KeyGenTimeout   time.Duration        `json:"key_gen_timeout" mapstructure:"key_gen_timeout"`     //底层 TSS keygen 超时。
	KeySignTimeout  time.Duration        `json:"key_sign_timeout" mapstructure:"key_sign_timeout"`   //底层 TSS keysign 超时。
	PreParamTimeout time.Duration        `json:"pre_param_timeout" mapstructure:"pre_param_timeout"` //生成 pre-parameters 的超时。
	GasLimitScaler  int                  `json:"gas_limit_scaler" mapstructure:"gas_limit_scaler"`   //以太坊交易估算 gas 时的缩放系数。
	Secrets         SecretsManagerConfig `json:"secrets" mapstructure:"secrets"`
	Shamir          ShamirConfig         `json:"shamir" mapstructure:"shamir"`
}

type SecretsManagerConfig struct {
	Enable   bool   `json:"enable" mapstructure:"enable"`       //是否启用 AWS Secrets Manager。
	SecretId string `json:"secret_id" mapstructure:"secret_id"` //存储本地状态用的 secret 名称或 ID。
}

type ShamirConfig struct {
	Enable bool      `json:"enable" mapstructure:"enable"` //是否启用 Shamir 分片方案。
	Kms    KmsConfig `json:"kms" mapstructure:"kms"`       //KMS 的 key、region 和访问凭证。
	S3     S3Config  `json:"s3" mapstructure:"s3"`         //S3 的 region、bucket 和访问凭证。
	Sm     SMConfig  `json:"sm" mapstructure:"sm"`         //Secrets Manager 的 region、secret 列表和访问凭证。
	Xor    string    `json:"xor" mapstructure:"xor"`       //分片额外 XOR 的密钥串。
}

// 这些都是 AWS 存储和加密相关的子配置：
type KmsConfig struct {
	KeyId  string     `json:"key_id" mapstructure:"key_id"` //KMS key id。
	Region string     `json:"region" mapstructure:"region"` //AWS 区域。
	Aksk   AKSKConfig `json:"aksk" mapstructure:"aksk"`
}

type S3Config struct {
	Region  string     `json:"region" mapstructure:"region"`
	Buckets string     `json:"buckets" mapstructure:"buckets"` //S3 bucket 列表，逗号分隔。
	Aksk    AKSKConfig `json:"aksk" mapstructure:"aksk"`
}

type SMConfig struct {
	Region    string     `json:"region" mapstructure:"region"`
	SecretIds string     `json:"secretIds" mapstructure:"secretIds"` //Secrets Manager secret 列表，逗号分隔。
	Aksk      AKSKConfig `json:"aksk" mapstructure:"aksk"`
}

type AKSKConfig struct {
	Id     string `json:"id" mapstructure:"id"`
	Secret string `json:"secret" mapstructure:"secret"`
}

func DefaultConfiguration() Configuration {
	return Configuration{
		TimedTaskInterval: "10s",
		Manager: ManagerConfig{
			KeygenTimeout:     "120s",
			CPKConfirmTimeout: "2h",
			AskTimeout:        "60s",
			SignTimeout:       "60s",
		},
		Node: NodeConfig{
			P2PPort:         "8000",
			KeyGenTimeout:   10 * time.Second,
			KeySignTimeout:  10 * time.Second,
			PreParamTimeout: 5 * time.Minute,
			GasLimitScaler:  2,
		},
	}
}

// LoadConfig 从配置文件和环境变量中加载系统配置。
// 加载顺序：默认配置 -> 配置文件 -> 环境变量覆盖。
func LoadConfig(file string) (Configuration, error) {
	// 直接按传入路径读取配置文件，避免 `xxx.docker.toml`
	// 这类多段文件名被错误截断成 `xxx`。
	viper.SetConfigFile(filepath.Clean(file))

	// 读取配置文件内容
	if err := viper.ReadInConfig(); err != nil {
		return Configuration{}, fmt.Errorf("fail to read from config file: %w", err)
	}

	viper.SetEnvPrefix("tss")
	viper.SetEnvKeyReplacer(
		strings.NewReplacer(".", "_", "-", "_"), // 将配置键中的 "." 和 "-" 转换为 "_"
	)
	viper.AutomaticEnv() // 自动读取匹配的环境变量

	// 先加载默认配置
	cfg := DefaultConfiguration()

	// 将配置文件和环境变量中的值反序列化到配置结构体
	if err := viper.Unmarshal(&cfg); err != nil {
		return Configuration{}, fmt.Errorf("fail to unmarshal: %w", err)
	}

	return cfg, nil
}

// GetConfigFromCmd 从 Cobra Command 的 Context 中获取配置对象。
//
// 配置对象通常在程序启动时解析完成，并通过 Context
// 传递给所有子命令，实现配置共享。
func GetConfigFromCmd(cmd *cobra.Command) Configuration {
	// 从 Context 中读取配置
	if v := cmd.Context().Value("config"); v != nil {
		clientCtxPtr := v.(*Configuration)
		return *clientCtxPtr
	}
	return Configuration{}
}

// SetCmdConfig 将配置写入 Cobra Command 的 Context。
func SetCmdConfig(cmd *cobra.Command, config Configuration) error {
	// 从 Context 获取配置指针
	v := cmd.Context().Value("config")
	if v == nil {
		return errors.New("client context not set")
	}

	// Context 中保存的是共享配置对象指针
	configPtr := v.(*Configuration)

	// 用新配置覆盖原配置
	// 因为是指针赋值，所以所有持有该配置对象的命令 都能看到最新配置
	*configPtr = config

	return nil
}
