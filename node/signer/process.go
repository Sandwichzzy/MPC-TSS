package signer

import (
	"crypto/ecdsa"
	"sync"
	"time"

	"github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/config"
	"github.com/Sandwichzzy/tss/node/tsslib"
	"github.com/Sandwichzzy/tss/node/types"
	"github.com/Sandwichzzy/tss/ws/client"
	ethc "github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	tdtypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

type Processor struct {
	localPubkey     string
	localPubKeyByte []byte
	address         ethc.Address
	privateKey      *ecdsa.PrivateKey
	wsClient        *client.WSClientWarp
	stopChan        chan struct{}
	wg              *sync.WaitGroup

	tssServer         tsslib.Server
	askRequestChan    chan tdtypes.RPCRequest
	signRequestChan   chan tdtypes.RPCRequest
	keygenRequestChan chan tdtypes.RPCRequest
	waitSignLock      *sync.RWMutex
	waitSignMsgs      map[string]common.TransactionSignRequest

	cacheVerifyLock *sync.RWMutex
	cacheVerify     *types.Cache[string, bool] //顺序插入的缓存
	cacheSignLock   *sync.RWMutex
	cacheSign       *types.Cache[string, []byte]

	nodeStore             types.NodeStore
	logger                zerolog.Logger
	taskInterval          time.Duration
	confirmReceiptTimeout time.Duration
	gasLimitScaler        int
	metrics               *Metrics
}

// NewProcessor builds the node-side signer and establishes the websocket client that will connect back to the Manager's `/ws` endpoint.
func NewProcessor(cfg config.Configuration, tssInstance tsslib.Server, privKey *ecdsa.PrivateKey, pubkeyByte []byte, pubKeyHex string, nodeStore types.NodeStore, address ethc.Address) (*Processor, error) {
	taskIntervalDur, err := time.ParseDuration(cfg.TimedTaskInterval)
	if err != nil {
		return nil, err
	}

	WSClient, err := client.NewWSClient(cfg.Node.WsAddr, "/ws", privKey, pubKeyHex)
	if err != nil {
		return nil, err
	}

	processor := &Processor{
		localPubkey:       pubKeyHex,
		localPubKeyByte:   pubkeyByte,
		address:           address,
		privateKey:        privKey,
		tssServer:         tssInstance,
		stopChan:          make(chan struct{}),
		wg:                &sync.WaitGroup{},
		logger:            log.With().Str("module", "signer").Logger(),
		wsClient:          WSClient,
		askRequestChan:    make(chan tdtypes.RPCRequest, 100),
		signRequestChan:   make(chan tdtypes.RPCRequest, 100),
		keygenRequestChan: make(chan tdtypes.RPCRequest, 1),
		waitSignLock:      &sync.RWMutex{},
		waitSignMsgs:      make(map[string]common.TransactionSignRequest),
		cacheVerifyLock:   &sync.RWMutex{},
		cacheVerify:       types.NewCache[string, bool](1000),
		cacheSignLock:     &sync.RWMutex{},
		cacheSign:         types.NewCache[string, []byte](10),
		nodeStore:         nodeStore,
		taskInterval:      taskIntervalDur,
		gasLimitScaler:    cfg.Node.GasLimitScaler,
		metrics:           PrometheusMetrics("tssnode"),
	}
	return processor, nil
}

func (p *Processor) Start() {
	p.logger.Info().Msg("Signer is starting")
	p.wg.Add(4)
	p.run()
}

func (p *Processor) Stop() {
	p.logger.Info().Msg("going to stop signer")
	defer p.logger.Info().Msg("signer stopped")
	close(p.stopChan)
	err := p.wsClient.Cli.Stop()
	if err != nil {
		return
	}
	p.wg.Wait()
}

func (p *Processor) run() {
	go p.ProcessMessage()
	go p.Verify()
	go p.Sign()
	go p.Keygen()
}
