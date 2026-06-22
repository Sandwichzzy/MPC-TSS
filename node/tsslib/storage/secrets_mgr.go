package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	bkeygen "github.com/bnb-chain/tss-lib/ecdsa/keygen"
	"github.com/rs/zerolog/log"

	"time"
)

// secrets_mgr.go 提供一个简单的云端托管模式：
// 把整个 `map[pubkey]KeygenLocalState` 序列化后放进一个 AWS SecretsManager secret。
const CtxTimeout = 30 * time.Second

type SecretsManager interface {
	Save() error
	PutKeyFile(state KeygenLocalState) error
	GetKeyFile(pubKey string) (KeygenLocalState, error)
}

type SecretsMgr struct {
	secretId string                      // 目标 secret 的名字或 ARN。
	keys     map[string]KeygenLocalState // 启动时先从 AWS 拉下来，后续先改内存，再统一 Save 回去。
	client   *secretsmanager.Client
}

// NewSecretsMgr 初始化时会把整个 secret 拉到内存，作为当前进程的工作副本。
func NewSecretsMgr(secretId string) (*SecretsMgr, error) {
	log.Debug().Msg("start to load default config for secrets manager")
	ctx, cancel := context.WithTimeout(context.Background(), CtxTimeout)
	defer cancel()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Error().Err(err).Msg("fail to load config for secrets manager")
		return nil, errors.New("fail to new secrets manager instance")
	}
	client := secretsmanager.NewFromConfig(cfg)
	params := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretId),
	}
	result, err := client.GetSecretValue(ctx, params)
	if err != nil {
		log.Error().Err(err).Msgf("fail to get secret value form aws secrets manager :%v", err)
		return nil, err
	}

	var keys map[string]KeygenLocalState
	if err := json.Unmarshal(result.SecretBinary, &keys); err != nil {
		log.Error().Err(err).Msgf("fail to unmarshal data to map :%v", err)
		keys = map[string]KeygenLocalState{}
	}
	return &SecretsMgr{
		secretId: secretId,
		keys:     keys,
		client:   client,
	}, nil
}

// PutKeyFile 只更新进程内的镜像，不立即提交到 AWS。
func (sm *SecretsMgr) PutKeyFile(stat KeygenLocalState) error {
	_, ok := sm.keys[stat.PubKey]
	if !ok {
		sm.keys[stat.PubKey] = stat
	}
	return nil
}

// GetKeyFile 直接从内存副本里按聚合公钥取状态。
func (sm *SecretsMgr) GetKeyFile(pubKey string) (KeygenLocalState, error) {
	value, ok := sm.keys[pubKey]
	if !ok {
		log.Warn().Msgf("can not find keygenlocalstate from storage by this pubKey (%s)", pubKey)
		return KeygenLocalState{}, nil
	}
	return value, nil
}

// GetOneLocalState 返回任意一份已有状态中的 LocalPreParams，供节点启动时复用预计算结果。
func (sm *SecretsMgr) GetOneLocalState() *bkeygen.LocalPreParams {
	var preParams *bkeygen.LocalPreParams
	if len(sm.keys) > 0 {
		for _, value := range sm.keys {
			preParams = &value.LocalData.LocalPreParams
			break
		}
	} else {
		return nil
	}
	return preParams
}

// Save 把当前 keys 全量覆盖写回 SecretsManager。
// 这不是增量更新某一项，而是更新整份 JSON。
func (sm *SecretsMgr) Save() error {
	if len(sm.keys) == 0 {
		log.Warn().Msg("secrets storage size is 0 ,don't need to put to aws secrets manager")
		return nil
	}
	buf, err := json.Marshal(sm.keys)
	if err != nil {
		log.Error().Err(err).Msgf("fail to marshal secrets keys map to json: %v", err)
		return err
	}
	params := &secretsmanager.UpdateSecretInput{
		SecretId:     aws.String(sm.secretId),
		SecretBinary: buf,
	}
	output, err := sm.client.UpdateSecret(context.TODO(), params)
	if err != nil {
		log.Error().Err(err).Msgf("fail to put data to aws's secrets manager : %v", err)
		return err
	}
	log.Info().Msgf("put data to aws's secrets manager success, version is:%v", output.VersionId)
	return nil
}
