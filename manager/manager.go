package manager

import (
	"fmt"
	"sync"
	"time"

	"github.com/Sandwichzzy/tss/common"
	tss "github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/config"
	"github.com/Sandwichzzy/tss/manager/metrics"
	"github.com/Sandwichzzy/tss/manager/types"
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
	//_,err:=m.keygen() todo:
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
	return nil, nil
}

func (m *Manager) Keygen(request tss.KeygenRequest) (string, error) {
	return "", nil
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
	return nil
}

func (m *Manager) getStateSignature(digestBz []byte) []byte {
	return nil
}

func (m *Manager) setStateSignature(digestBz []byte, sig []byte) {
}
