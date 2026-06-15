package manager

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Sandwichzzy/tss/common"
	tss "github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/config"
	"github.com/Sandwichzzy/tss/manager/metrics"
	"github.com/Sandwichzzy/tss/manager/types"
	"github.com/Sandwichzzy/tss/message"
	"github.com/Sandwichzzy/tss/ws/server"
	"github.com/ethereum/go-ethereum/log"
)

type Manager struct {
	wsServer            server.IWebsocketManager
	store               types.IManagerStore
	tssMembers          []string
	taskInterval        time.Duration
	keygenTimeout       time.Duration
	cpkConfirmTimeout   time.Duration
	askTimeout          time.Duration
	signTimeout         time.Duration
	stateSignatureCache map[[32]byte][]byte
	sigCacheLock        *sync.RWMutex
	stopGenKey          bool
	stopChan            chan struct{}
	metrics             *metrics.Metrics
}

func NewManager(wsServer server.IWebsocketManager, store types.IManagerStore, config config.Configuration) (*Manager, error) {
	taskIntervalDur, err := time.ParseDuration(config.TimedTaskInterval)
	if err != nil {
		return nil, err
	}

	keygenTimeoutDur, err := time.ParseDuration(config.Manager.KeygenTimeout)
	if err != nil {
		return nil, err
	}

	cpkConfirmTimeoutDur, err := time.ParseDuration(config.Manager.CPKConfirmTimeout)
	if err != nil {
		return nil, err
	}

	askTimeoutDur, err := time.ParseDuration(config.Manager.AskTimeout)
	if err != nil {
		return nil, err
	}

	signTimeoutDur, err := time.ParseDuration(config.Manager.SignTimeout)
	if err != nil {
		return nil, err
	}

	return &Manager{
		wsServer:            wsServer,
		tssMembers:          config.Manager.Nodes,
		store:               store,
		taskInterval:        taskIntervalDur,
		keygenTimeout:       keygenTimeoutDur,
		cpkConfirmTimeout:   cpkConfirmTimeoutDur,
		askTimeout:          askTimeoutDur,
		signTimeout:         signTimeoutDur,
		stateSignatureCache: make(map[[32]byte][]byte),
		sigCacheLock:        &sync.RWMutex{},
		stopChan:            make(chan struct{}),
		metrics:             metrics.PrometheusMetrics("tssmanager"),
	}, nil
}

func (m *Manager) Start() {
	log.Info("manager is starting......")
	_, err := m.keygen(nil, 4, 12, "") //todo: 补充参数
	if err != nil {
		log.Error("keygen fail", "err", err)
		return
	}
}

func (m *Manager) Stop() {
	close(m.stopChan)
}

func (m *Manager) stopGenerateKey() {
	m.stopGenKey = true
}

func (m *Manager) recoverGenerateKey() {
	m.stopGenKey = false
}

func (m *Manager) TransactionSign(request tss.TransactionSignRequest) ([]byte, error) {
	log.Info("received sign state request", "message hash", request.MessageHash)
	digestBz, err := hex.DecodeString(request.MessageHash)
	if err != nil {
		return nil, err
	}
	//check sig cache first
	if sig := m.getStateSignature(digestBz); len(sig) > 0 {
		log.Info("get stored signature ", "digest", hex.EncodeToString(digestBz), "sig", hex.EncodeToString(sig))
		response := tss.BatchSubmitterResponse{
			Signature: sig,
		}
		responseBytes, err := json.Marshal(response)
		if err != nil {
			log.Error("batch submitter response failed to marshal !")
			return nil, err
		}
		return responseBytes, nil
	}

	ctx := types.NewContext().WithRequestId(request.RequestId).WithElectionId(request.ElectionId)

	// ask tss for nodes the agreement
	ctx, err = m.agreement(ctx, request, tss.AskMessageHash)
	if err != nil {
		m.metrics.SignCount.Add(1) //签名失败数量+1
		return nil, err
	}
	var resp tss.SignResponse
	var signErr error

	m.metrics.ApproveNumber.Set(float64(len(ctx.Approvers())))

	resp, signErr = m.sign(ctx, request, digestBz, tss.TransactionSign)
	if signErr != nil {
		m.metrics.SignCount.Add(1)
		return nil, signErr
	}

	m.metrics.SignCount.Set(0)
	absents := make([]string, 0)
	if err = m.afterTransactionSign(ctx, request.MessageHash, absents); err != nil {
		log.Error("failed to execute afterSign", "err", err)
	}
	m.setStateSignature(digestBz, resp.Signature)

	response := tss.BatchSubmitterResponse{
		Signature: resp.Signature,
	}
	responseBytes, err := json.Marshal(response)
	if err != nil {
		log.Error("batch submitter response failed to marshal !")
		return nil, err
	}
	return responseBytes, nil
}

func (m *Manager) Keygen(request tss.KeygenRequest) (string, error) {
	cpk, err := m.generateKey(m.tssMembers, request.Threshold, request.ElectionId, request.RequestId)
	if err != nil {
		log.Error("failed to generate key", "err", err)
		return "0x", err
	}
	if err = m.store.Insert(types.CpkData{
		Cpk:          cpk,
		ElectionId:   request.ElectionId,
		CreationTime: time.Now(),
	}); err != nil {
		log.Error("failed to get cpk from storage", "err", err)
		return "0x", err
	}
	log.Info("cpk generate", "cpk", cpk)
	return cpk, nil
}

func (m *Manager) availableNodes(tssMembers []string) []string {
	aliveNodes := m.wsServer.AliveNodes()
	m.metrics.OnlineNodesCount.Set(float64(len(aliveNodes)))
	log.Info("check available nodes", "expected", fmt.Sprintf("%v", tssMembers), "alive nodes", fmt.Sprintf("%v", aliveNodes))

	availableNodes := make([]string, 0)
	for _, node := range aliveNodes {
		if common.ExistsIgnoreCase(tssMembers, node) {
			availableNodes = append(availableNodes, node)
		}
	}
	return availableNodes
}

func (m *Manager) afterTransactionSign(ctx types.Context, MessageHash string, absentNodes []string) error {
	sbi := message.MessageHashInfo{
		MessageHash:  MessageHash,
		ElectionId:   ctx.ElectionId(),
		AbsentNodes:  absentNodes,
		WorkingNodes: ctx.AvailableNodes(),
	}
	log.Info("Store the signed state batch", " messageHash", MessageHash)
	if err := m.store.SetMessageHash(sbi); err != nil {
		return err
	}
	return nil
}

func (m *Manager) getStateSignature(digestBz []byte) []byte {
	m.sigCacheLock.RLock()
	defer m.sigCacheLock.RUnlock()
	var key [32]byte
	copy(key[:], digestBz)
	return m.stateSignatureCache[key]
}

func (m *Manager) setStateSignature(digestBz []byte, sig []byte) {
	m.sigCacheLock.Lock()
	defer m.sigCacheLock.Unlock()
	for key := range m.stateSignatureCache {
		delete(m.stateSignatureCache, key)
	}
	var key [32]byte
	copy(key[:], digestBz)
	m.stateSignatureCache[key] = sig
}
