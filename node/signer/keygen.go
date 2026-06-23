package signer

import (
	"encoding/json"

	tsscommon "github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/node/tsslib/common"
	"github.com/Sandwichzzy/tss/node/tsslib/keygen"
	tmtypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

// Keygen() 聚合公钥
// 1. 解析 `KeygenRequest`
// 2. 组装 `keygen.Request`
// 3. 调用 `p.tssServer.Keygen(...)`
// 4. 把返回的聚合公钥包装成 `KeygenResponse`
// 5. 通过 WebSocket 回给 Manager

func (p *Processor) Keygen() {
	logger := p.logger.With().Str("step", "keygen").Logger()
	logger.Info().Msg("start to keygen ")

	p.launchTracked(func() {
		defer func() {
			logger.Info().Msg("exit keygen process")
		}()
		for {
			select {
			case <-p.stopChan:
				return
			case req := <-p.keygenRequestChan:
				var resId = req.ID.(tmtypes.JSONRPCStringID).String()
				logger.Info().Msgf("dealing resId (%s) ", resId)

				var keyR tsscommon.KeygenRequest
				if err := json.Unmarshal(req.Params, &keyR); err != nil {
					logger.Error().Msg("failed to unmarshal ask request")
					RpcResponse := tmtypes.NewRPCErrorResponse(req.ID, 201, "failed", err.Error())
					if err = p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
					continue
				}
				keygenReq := keygen.NewRequest(keyR.Nodes, keyR.Threshold)
				resp, err := p.tssServer.Keygen(keygenReq)
				if err != nil {
					logger.Err(err).Msg("failed to keygen !")
					RpcResponse := tmtypes.NewRPCErrorResponse(req.ID, 202, "failed", err.Error())
					if err := p.wsClient.SendMsg(RpcResponse); err != nil {
						logger.Error().Err(err).Msg("failed to send msg to manager")
					}
				} else {
					if resp.Status == common.Success {
						keygenResponse := tsscommon.KeygenResponse{
							ClusterPublicKey: resp.PubKey,
						}
						RpcResponse := tmtypes.NewRPCSuccessResponse(tmtypes.JSONRPCStringID(resId), keygenResponse)
						if err := p.wsClient.SendMsg(RpcResponse); err != nil {
							logger.Error().Err(err).Msg("failed to send msg to manager")
						}
						logger.Info().Msgf("keygen success cpk=(%s)", resp.PubKey)
						// todo: 将 key 存储到 levelDB
					} else {
						RpcResponse := tmtypes.NewRPCErrorResponse(req.ID, 202, "failed", resp.FailReason)
						if err := p.wsClient.SendMsg(RpcResponse); err != nil {
							logger.Error().Err(err).Msg("failed to send msg to manager")
						}
					}
				}

			}
		}
	})
}
