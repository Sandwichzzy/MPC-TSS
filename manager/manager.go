package manager

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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
	stateSignatureCache map[string][]byte
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
		stateSignatureCache: make(map[string][]byte),
		sigCacheLock:        &sync.RWMutex{},
		stopChan:            make(chan struct{}),
		metrics:             metrics.PrometheusMetrics("tssmanager"),
	}, nil
}

func (m *Manager) Start() {
	log.Info("manager is starting......")
	log.Info("manager is ready, use /api/v1/keygen to create a cluster public key")
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

	cpkData, err := m.store.GetByElectionId(request.ElectionId)
	if err != nil {
		return nil, err
	}
	if len(cpkData.Cpk) == 0 {
		return nil, fmt.Errorf("cluster public key not found for election_id %d", request.ElectionId)
	}
	//check sig cache first
	if sig := m.getStateSignature(digestBz, request.ElectionId, cpkData.Cpk); len(sig) > 0 {
		log.Info("get stored signature ", "digest", hex.EncodeToString(digestBz), "sig", hex.EncodeToString(sig), "election_id", request.ElectionId)
		response := tss.DigestSignResponse{
			Signature: sig,
		}
		responseBytes, err := json.Marshal(response)
		if err != nil {
			log.Error("digest sign response failed to marshal")
			return nil, err
		}
		return responseBytes, nil
	}

	members := cpkData.TssMembers
	if len(members) == 0 {
		members = m.tssMembers
	}
	availableNodes := m.availableNodes(members)
	ctx := types.NewContext().
		WithRequestId(request.RequestId).
		WithElectionId(request.ElectionId).
		WithAvailableNodes(availableNodes).
		WithTssInfo(&types.TssCommitteeInfo{
			ElectionId:    request.ElectionId,
			ClusterPubKey: cpkData.Cpk,
			TssMembers:    members,
			Threshold:     cpkData.Threshold,
		})

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
	if err = m.recordSignedDigest(ctx, request.MessageHash, absents); err != nil {
		log.Error("failed to execute afterSign", "err", err)
	}
	m.setStateSignature(digestBz, request.ElectionId, cpkData.Cpk, resp.Signature)

	response := tss.DigestSignResponse{
		Signature: resp.Signature,
	}
	responseBytes, err := json.Marshal(response)
	if err != nil {
		log.Error("digest sign response failed to marshal")
		return nil, err
	}
	return responseBytes, nil
}

func (m *Manager) Keygen(request tss.KeygenRequest) (string, error) {
	tssMembers := request.Nodes
	if len(tssMembers) == 0 {
		tssMembers = m.tssMembers
	}
	cpk, err := m.generateKey(tssMembers, request.Threshold, request.ElectionId, request.RequestId)
	if err != nil {
		log.Error("failed to generate key", "err", err)
		return "0x", err
	}
	if err = m.store.Insert(types.CpkData{
		Cpk:          cpk,
		ElectionId:   request.ElectionId,
		Threshold:    request.Threshold,
		TssMembers:   tssMembers,
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

func (m *Manager) recordSignedDigest(ctx types.Context, messageHash string, absentNodes []string) error {
	info := message.SignedDigestInfo{
		MessageHash:  messageHash,
		ElectionId:   ctx.ElectionId(),
		AbsentNodes:  absentNodes,
		WorkingNodes: ctx.AvailableNodes(),
	}
	log.Info("store signed digest metadata", "message_hash", messageHash)
	if err := m.store.SetSignedDigest(info); err != nil {
		return err
	}
	return nil
}

func buildStateSignatureCacheKey(digestBz []byte, electionId uint64, clusterPublicKey string) string {
	normalizedClusterKey := strings.TrimSpace(clusterPublicKey)
	normalizedClusterKey = strings.TrimPrefix(normalizedClusterKey, "0x")
	normalizedClusterKey = strings.TrimPrefix(normalizedClusterKey, "0X")
	normalizedClusterKey = strings.ToLower(normalizedClusterKey)

	return fmt.Sprintf("%s:%d:%s", hex.EncodeToString(digestBz), electionId, normalizedClusterKey)
}

func (m *Manager) getStateSignature(digestBz []byte, electionId uint64, clusterPublicKey string) []byte {
	m.sigCacheLock.RLock()
	defer m.sigCacheLock.RUnlock()
	key := buildStateSignatureCacheKey(digestBz, electionId, clusterPublicKey)
	return m.stateSignatureCache[key]
}

func (m *Manager) setStateSignature(digestBz []byte, electionId uint64, clusterPublicKey string, sig []byte) {
	m.sigCacheLock.Lock()
	defer m.sigCacheLock.Unlock()
	for key := range m.stateSignatureCache {
		delete(m.stateSignatureCache, key)
	}
	key := buildStateSignatureCacheKey(digestBz, electionId, clusterPublicKey)
	m.stateSignatureCache[key] = sig
}
