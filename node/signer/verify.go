package signer

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Sandwichzzy/tss/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	tmtypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

// Verify 处理 ask 阶段请求, 只做“是否接受进入签名流程”的确认。
// 它主要做这些事情：
//
// 1. 解析 `TransactionSignRequest`
// 2. 校验请求字段和 `message_hash` 格式
// 3. 把通过校验的消息哈希记入 `waitSignMsgs`
// 4. 回一个 `AskResponse{Result: true}`
func (p *Processor) Verify() {
	defer p.wg.Done()
	logger := p.logger.With().Str("step", "verify event").Logger()
	logger.Info().Msg("start to verify events ")

	go func() {
		defer func() {
			logger.Info().Msg("exit verify event process")
		}()
		for {
			select {
			case <-p.stopChan:
				return
			case req := <-p.askRequestChan:
				var askRequest common.TransactionSignRequest
				if err := json.Unmarshal(req.Params, &askRequest); err != nil {
					logger.Error().Msg("failed to unmarshal ask request")
					rpcResponse := tmtypes.NewRPCErrorResponse(req.ID, 201, "failed to unmarshal", err.Error())
					if err := p.wsClient.SendMsg(rpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					continue
				}
				hashStr, err := validateAskRequest(askRequest)
				if err != nil {
					logger.Error().Err(err).Msg("invalid ask request")
					rpcResponse := tmtypes.NewRPCErrorResponse(req.ID, 201, "invalid askRequest", err.Error())
					if err := p.wsClient.SendMsg(rpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					continue
				}

				p.UpdateWaitSignEvents(hashStr, askRequest)

				askResponse := common.AskResponse{
					Result: true,
				}
				rpcResponse := tmtypes.NewRPCSuccessResponse(req.ID, askResponse)
				if err := p.wsClient.SendMsg(rpcResponse); err != nil {
					logger.Error().Err(err).Msg("failed to send msg to manager")
				}
			}
		}
	}()
}

func validateAskRequest(askRequest common.TransactionSignRequest) (string, error) {
	if askRequest.MessageHash == "" {
		return "", fmt.Errorf("message_hash must not be empty")
	}
	if askRequest.ElectionId == 0 {
		return "", fmt.Errorf("election_id must be greater than 0")
	}

	hash, err := hex.DecodeString(askRequest.MessageHash)
	if err != nil {
		return "", fmt.Errorf("message_hash must be valid hex: %w", err)
	}
	if len(hash) != 32 {
		return "", fmt.Errorf("message_hash must decode to 32 bytes, got %d", len(hash))
	}

	return hexutil.Encode(hash), nil
}

func (p *Processor) UpdateWaitSignEvents(uniqueId string, msg common.TransactionSignRequest) {
	p.waitSignLock.Lock()
	defer p.waitSignLock.Unlock()
	p.waitSignMsgs[uniqueId] = msg
}

func (p *Processor) CacheVerify(key string, value bool) bool {
	p.cacheVerifyLock.Lock()
	defer p.cacheVerifyLock.Unlock()
	return p.cacheVerify.Set(key, value)
}

func (p *Processor) GetVerify(key string) (bool, bool) {
	p.cacheVerifyLock.RLock()
	defer p.cacheVerifyLock.RUnlock()
	return p.cacheVerify.Get(key)
}
