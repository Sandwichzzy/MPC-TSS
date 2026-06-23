package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/node/tsslib/conversion"
	"github.com/Sandwichzzy/tss/node/tsslib/messages"
	"github.com/Sandwichzzy/tss/node/types"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	maddr "github.com/multiformats/go-multiaddr"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// 所有 TSS 节点之间通信都走这个 protocol
var TSSProtocolID protocol.ID = "/p2p/tss"

const (
	TimeoutConnecting = time.Second * 20 // TimeoutConnecting maximum time for wait for peers to connect
)

// Communication is the core TSS P2P communication layer
// 负责：
// 1. libp2p host 管理
// 2. stream 读写
// 3. peer discovery
// 4. pub/sub（按 MsgID 订阅）
// 5. broadcast 分发
type Communication struct {
	bootstrapPeers    []maddr.Multiaddr // bootstrapPeers are initial known nodes for network joining
	waitFullConnected bool              // waitFullConnected means whether we wait ALL bootstrap nodes online
	logger            zerolog.Logger
	listenAddr        maddr.Multiaddr // libp2p listening address
	host              host.Host       // libp2p host (core networking object)
	wg                *sync.WaitGroup
	stopChan          chan struct{}
	lifecycleLocker   *sync.Mutex
	stopping          bool
	subscribers       map[messages.TSSMessageTpe]*MessageIDSubscriber // subscriber map: topic(TSSMessageType) -> MsgID -> channel
	subscriberLocker  *sync.Mutex
	streamCount       int64
	BroadcastMsgChan  chan *messages.BroadcastMsgChan // channel for async broadcast pipeline
	externalAddr      maddr.Multiaddr                 // external address (NAT/public IP override)
	streamMgr         *StreamMgr                      // stream manager: manage msgID -> []streams mapping
	tssMemberStore    types.TssMemberStore            // store active/inactive TSS members
}

func NewCommunication(
	bootstrapPeers []maddr.Multiaddr,
	port int,
	externalIP string,
	waitFullConnected bool,
	store types.TssMemberStore,
) (*Communication, error) {
	// build listen address: 0.0.0.0:port
	addr, err := maddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port))
	if err != nil {
		return nil, fmt.Errorf("fail to create listen addr: %w", err)
	}
	// optional external IP (used in NAT / cloud env)
	var externalAddr maddr.Multiaddr = nil
	if len(externalIP) != 0 {
		externalAddr, err = maddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%d", externalIP, port))
		if err != nil {
			return nil, fmt.Errorf("fail to create listen with given external IP: %w", err)
		}
	}

	return &Communication{
		bootstrapPeers:    bootstrapPeers,
		logger:            log.With().Str("module", "communication").Logger(),
		listenAddr:        addr,
		wg:                &sync.WaitGroup{},
		stopChan:          make(chan struct{}),
		lifecycleLocker:   &sync.Mutex{},
		subscribers:       make(map[messages.TSSMessageTpe]*MessageIDSubscriber),
		subscriberLocker:  &sync.Mutex{},
		streamCount:       0,
		BroadcastMsgChan:  make(chan *messages.BroadcastMsgChan, 1024),
		externalAddr:      externalAddr,
		streamMgr:         NewStreamMgr(),
		waitFullConnected: waitFullConnected,
		tssMemberStore:    store,
	}, nil
}

// ==============================
// Host / Identity helpers
// ==============================

func (c *Communication) GetHost() host.Host {
	return c.host
}

// GetLocalPeerID returns current node peer ID
func (c *Communication) GetLocalPeerID() string {
	return c.host.ID().String()
}

// ==============================
// Broadcast entrypoint
// ==============================

// Broadcast sends message to multiple peers concurrently
// msgID: used for response correlation
func (c *Communication) Broadcast(peers []peer.ID, msg []byte, msgID string, needHandleResponse bool) {
	if len(peers) == 0 {
		return
	}
	if !c.tryTrackBroadcast() {
		c.logger.Debug().Msgf("skip broadcast for msgID %s because communication is stopping", msgID)
		return
	}
	// try to discover all peers and then broadcast the messages
	go c.broadcastToPeers(peers, msg, msgID, needHandleResponse)
}

// ==============================
// Response handler
// ==============================

// HandleResponse reads response from stream
func (c *Communication) HandleResponse(stream network.Stream, remotePeer peer.ID) error {
	// read response payload
	_, err := ReadStreamWithBuffer(stream)
	if err != nil {
		// peer unhealthy -> disconnect
		c.logger.Error().Err(err).Msgf("fail to get response from %s", remotePeer.String())

		c.disconnect(remotePeer)
		return err
	}
	return nil
}

// disconnect closes all connections to a peer
func (c *Communication) disconnect(remotePeer peer.ID) {
	cns := c.host.Network().ConnsToPeer(remotePeer)
	for _, cn := range cns {
		if err := cn.Close(); err != nil {
			c.logger.Err(err).Msgf("fail to close peer: %s", remotePeer)
		}
	}
}

// ==============================
// Internal broadcast pipeline
// ==============================

// fan-out broadcast to peers
func (c *Communication) broadcastToPeers(peers []peer.ID, msg []byte, msgID string, needHandleResponse bool) {
	defer c.wg.Done()
	defer func() {
		c.logger.Debug().Msgf("finished sending message to peer(%v)", peers)
	}()
	var wgSend sync.WaitGroup
	wgSend.Add(len(peers))
	for _, p := range peers {
		go func(p peer.ID) {
			defer wgSend.Done()
			if err := c.writeToStream(p, msg, msgID, needHandleResponse); nil != err {
				c.logger.Error().Err(err).Msg("fail to write to stream")
			}
		}(p)
	}
	wgSend.Wait()
}

// ==============================
// Stream write (core send logic)
// ==============================

// writeToStream sends message to one peer
func (c *Communication) writeToStream(pID peer.ID, msg []byte, msgID string, needHandleResponse bool) error {
	// avoid self-send
	if pID == c.host.ID() {
		return nil
	}
	stream, err := c.connectToOnePeer(pID)
	if err != nil {
		return fmt.Errorf("fail to open stream: %w", err)
	}
	if stream == nil {
		return nil
	}
	// send payload
	if err = WriteStreamWithBuffer(msg, stream); err != nil {
		_ = stream.Reset()
		return fmt.Errorf("fail to write msg: %w", err)
	}
	c.streamMgr.AddStream(msgID, stream)
	// optionally wait response
	if needHandleResponse {
		if err := c.HandleResponse(stream, pID); err != nil {
			_ = stream.Reset()
			// retry once (fail-safe)
			return c.writeToStream(pID, msg, msgID, false)
		}
	}
	return nil
}

// ==============================
// Stream read (incoming handler)
// ==============================

// readFromStream handles incoming stream data
func (c *Communication) readFromStream(stream network.Stream) {
	peerID := stream.Conn().RemotePeer().String()
	c.logger.Debug().Msgf("reading from stream of peer: %s", peerID)

	select {
	case <-c.stopChan:
		return
	default:
		dataBuf, err := ReadStreamWithBuffer(stream)
		// send ACK back asynchronously
		go WriteStreamWithBuffer([]byte("ack"), stream)

		if err != nil {
			c.logger.Error().Err(err).Msgf("fail to read from stream,peerID: %s", peerID)
			c.streamMgr.AddStream("UNKNOWN", stream)
			return
		}

		// unwrap TSS message
		var wrappedMsg messages.WrappedMessage
		if err := json.Unmarshal(dataBuf, &wrappedMsg); err != nil {
			c.logger.Error().Err(err).Msg("fail to unmarshal wrapped message bytes")
			c.streamMgr.AddStream("UNKNOWN", stream)
			return
		}

		// register stream by MsgID
		c.streamMgr.AddStream(wrappedMsg.MsgID, stream)

		// retry subscription lookup (race-safe)
		for i := 1; i < 4; i++ {
			channel := c.getSubscriber(wrappedMsg.MessageType, wrappedMsg.MsgID)
			if channel != nil {
				c.logger.Debug().Msgf("from peer (%s) ,MsgId (%s), MsgType (%s)", peerID, wrappedMsg.MsgID, wrappedMsg.MessageType)
				channel <- &Message{
					PeerID:  stream.Conn().RemotePeer(),
					Payload: dataBuf,
				}
				break
			} else {
				c.logger.Debug().Msgf("no MsgID %s found for this message,need to retry %d time", wrappedMsg.MsgID, i)
				c.logger.Debug().Msgf("no MessageType %s found for this message,need to retry %d time", wrappedMsg.MessageType, i)
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
}

// handleStream is libp2p stream entrypoint
func (c *Communication) handleStream(stream network.Stream) {

	peerID := stream.Conn().RemotePeer().String()
	c.logger.Debug().Msgf("handle stream from peer: %s", peerID)

	// security check: only trusted nodes allowed
	if c.VerifyPeerId(peerID) {
		c.readFromStream(stream)
	}
}

// ==============================
// Peer discovery / bootstrap
// ==============================

// bootStrapConnectivityCheck verifies bootstrap health
func (c *Communication) bootStrapConnectivityCheck() error {
	if len(c.bootstrapPeers) == 0 {
		c.logger.Error().Msg("we do not have the bootstrap node set, quit the connectivity check")
		return nil
	}

	var onlineNodes uint32
	var wg sync.WaitGroup

	for _, el := range c.bootstrapPeers {
		peerInfo, err := peer.AddrInfoFromP2pAddr(el)
		if err != nil {
			c.logger.Error().Err(err).Msg("error in decode the bootstrap node, skip it")
			continue
		}
		wg.Add(1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			defer wg.Done()
			outChan := ping.Ping(ctx, c.host, peerInfo.ID)
			select {
			case ret := <-outChan:
				if ret.Error == nil {
					c.logger.Debug().Msgf("connect to peer %v with RTT %v\n", peerInfo.ID, ret.RTT)
					atomic.AddUint32(&onlineNodes, 1)
				}
			case <-ctx.Done():
				c.logger.Error().Msgf("fail to ping the node %s within 2 seconds", peerInfo.ID)
			}
		}()
	}

	wg.Wait()

	if onlineNodes > 0 {
		c.logger.Info().Msgf("we have successfully ping pong %d nodes", onlineNodes)
		return nil
	}

	c.logger.Error().Msg("fail to ping any bootstrap node")
	return errors.New("the node cannot ping any bootstrap node")
}

// ==============================
// libp2p initialization
// ==============================

// 1. 用节点私钥创建 libp2p host
// 2. 注册 `/p2p/tss` 的 stream handler
// 3. 启动 Kademlia DHT
// 4. 尝试连接 bootstrap peers
// 5. 用 rendezvous 广播自身
// 6. 做 bootstrap connectivity check
func (c *Communication) startChannel(privKeyBytes []byte) error {
	ctx := context.Background()
	// load private key
	p2pPriKey, err := crypto.UnmarshalSecp256k1PrivateKey(privKeyBytes)
	if err != nil {
		c.logger.Error().Msgf("error is %f", err)
		return err
	}
	// NAT / external IP override
	addressFactory := func(addrs []maddr.Multiaddr) []maddr.Multiaddr {
		if c.externalAddr != nil {
			return []maddr.Multiaddr{c.externalAddr}
		}
		return addrs
	}

	// create libp2p host
	h, err := libp2p.New(
		libp2p.ListenAddrs([]maddr.Multiaddr{c.listenAddr}...),
		libp2p.Identity(p2pPriKey),
		libp2p.AddrsFactory(addressFactory),
	)
	if err != nil {
		return fmt.Errorf("fail to create p2p host: %w", err)
	}
	c.host = h
	c.logger.Info().Msgf("Host created, we are: %s, at: %s", h.ID(), h.Addrs())
	// register stream handler
	h.SetStreamHandler(TSSProtocolID, c.handleStream)
	// Start a DHT, for use in peer discovery. We can't just make a new DHT
	// client because we want each peer to maintain its own local copy of the
	// DHT, so that the bootstrapping node of the DHT can go down without
	// inhibiting future peer discovery.
	// create Kademlia DHT (peer discovery layer)
	kademliaDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeServer))
	if err != nil {
		return fmt.Errorf("fail to create DHT: %w", err)
	}
	c.logger.Debug().Msg("Bootstrapping the DHT")
	if err = kademliaDHT.Bootstrap(ctx); err != nil {
		return fmt.Errorf("fail to bootstrap DHT: %w", err)
	}

	var connectionErr error
	// connect bootstrap peers
	for i := 0; i < 60; i++ {
		connectionErr = c.connectToBootstrapPeers()
		if connectionErr == nil {
			break
		}
		c.logger.Error().Msg("cannot connect to bootstrap node, retry in 5 seconds")
		time.Sleep(time.Second * 5)
	}
	if connectionErr != nil {
		return fmt.Errorf("fail to connect to bootstrap peer: %w", connectionErr)
	}

	// advertise presence (rendezvous discovery)
	routingDiscovery := discovery.NewRoutingDiscovery(kademliaDHT)
	_, err = routingDiscovery.Advertise(ctx, "rendezvous")
	if err != nil {
		return err
	}

	err = c.bootStrapConnectivityCheck()
	if err != nil {
		return err
	}

	c.logger.Info().Msg("Successfully announced!")
	return nil
}

// ==============================
// peer dialing
// ==============================

// connectToOnePeer opens a libp2p stream to peer
func (c *Communication) connectToOnePeer(pID peer.ID) (network.Stream, error) {
	if pID == c.host.ID() {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), TimeoutConnecting)
	defer cancel()
	return c.host.NewStream(ctx, pID, TSSProtocolID)
}

// ==============================
// bootstrap connect
// ==============================
func (c *Communication) connectToBootstrapPeers() error {
	// Let's connect to the bootstrap nodes first. They will tell us about the
	// other nodes in the network.
	if len(c.bootstrapPeers) == 0 {
		c.logger.Info().Msg("no bootstrap node set, we skip the connection")
		return nil
	}
	var wg sync.WaitGroup
	connRet := make(chan bool, len(c.bootstrapPeers))
	for _, peerAddr := range c.bootstrapPeers {
		pi, err := peer.AddrInfoFromP2pAddr(peerAddr)
		if err != nil {
			return fmt.Errorf("fail to add peer: %w", err)
		}
		wg.Add(1)
		go func(connRet chan bool) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), TimeoutConnecting)
			defer cancel()
			if err := c.host.Connect(ctx, *pi); err != nil {
				c.logger.Error().Err(err).Msgf("fail to connect to %s", pi.String())
				connRet <- false
				return
			}
			connRet <- true
			c.logger.Info().Msgf("Connection established with bootstrap node: %s", *pi)
		}(connRet)
	}
	wg.Wait()

	var hasFail bool
	for i := 0; i < len(c.bootstrapPeers); i++ {
		if <-connRet {
			if !c.waitFullConnected {
				return nil
			}
		} else {
			hasFail = true
		}
	}
	if hasFail {
		return errors.New("fail to connect to peer")
	}
	return nil
}

// ==============================
// lifecycle
// ==============================

// Start starts libp2p + broadcast worker
func (c *Communication) Start(priKeyBytes []byte) error {
	err := c.startChannel(priKeyBytes)
	if err == nil {
		c.wg.Add(1)
		go c.ProcessBroadcast() //启动 `ProcessBroadcast()` 消费出站广播队列
	}
	return err
}

// Stop gracefully shuts down P2P system
func (c *Communication) Stop() error {
	c.beginStop()
	var err error
	if c.host != nil {
		err = c.host.Close()
	}
	c.wg.Wait()
	return err
}

func (c *Communication) tryTrackBroadcast() bool {
	c.lifecycleLocker.Lock()
	defer c.lifecycleLocker.Unlock()
	if c.stopping {
		return false
	}
	c.wg.Add(1)
	return true
}

func (c *Communication) beginStop() {
	c.lifecycleLocker.Lock()
	defer c.lifecycleLocker.Unlock()
	if c.stopping {
		return
	}
	c.stopping = true
	close(c.stopChan)
}

func (c *Communication) SetSubscribe(topic messages.TSSMessageTpe, msgID string, channel chan *Message) {
	c.subscriberLocker.Lock()
	defer c.subscriberLocker.Unlock()

	messageIDSubscribers, ok := c.subscribers[topic]
	if !ok {
		messageIDSubscribers = NewMessageIDSubscriber()
		c.subscribers[topic] = messageIDSubscribers
	}
	messageIDSubscribers.Subscribe(msgID, channel)
}

func (c *Communication) getSubscriber(topic messages.TSSMessageTpe, msgID string) chan *Message {
	c.subscriberLocker.Lock()
	defer c.subscriberLocker.Unlock()
	messageIDSubscribers, ok := c.subscribers[topic]
	if !ok {
		c.logger.Debug().Msgf("msgId %s fail to find subscribers for %s", msgID, topic)
		return nil
	}
	return messageIDSubscribers.GetSubscriber(msgID)
}

func (c *Communication) CancelSubscribe(topic messages.TSSMessageTpe, msgID string) {
	c.subscriberLocker.Lock()
	defer c.subscriberLocker.Unlock()

	messageIDSubscribers, ok := c.subscribers[topic]
	if !ok {
		c.logger.Debug().Msgf("cannot find the given channels %s", topic.String())
		return
	}
	if nil == messageIDSubscribers {
		return
	}
	messageIDSubscribers.UnSubscribe(msgID)
	if messageIDSubscribers.IsEmpty() {
		delete(c.subscribers, topic)
	}
}

func (c *Communication) ProcessBroadcast() {
	c.logger.Debug().Msg("start to process broadcast message channel")
	defer c.logger.Debug().Msg("stop process broadcast message channel")
	defer c.wg.Done()
	for {
		select {
		case msg := <-c.BroadcastMsgChan:
			wrappedMsgBytes, err := json.Marshal(msg.WrappedMessage)
			if err != nil {
				c.logger.Error().Err(err).Msg("fail to marshal a wrapped message to json bytes")
				continue
			}
			//c.logger.Debug().Msgf("broadcast message %s to %+v", msg.WrappedMessage, msg.PeersID)
			needHandleResponse := true
			if msg.WrappedMessage.MessageType == messages.TSSTaskDone {
				needHandleResponse = false
			}
			c.Broadcast(msg.PeersID, wrappedMsgBytes, msg.WrappedMessage.MsgID, needHandleResponse)

		case <-c.stopChan:
			return
		}
	}
}

func (c *Communication) ReleaseStream(msgID string) {
	c.streamMgr.ReleaseStream(msgID)
}

// ==============================
// security: peer validation
// ==============================

func (c *Communication) VerifyPeerId(peerId string) bool {
	activeNodes, err := c.tssMemberStore.GetActiveMembers()
	if err != nil {
		c.logger.Error().Err(err).Msg("failed to get active members from level db")
		return false
	}
	pubKey, err := conversion.GetPubKeyFromPeerID(peerId)
	if err != nil {
		c.logger.Error().Err(err).Msg("failed to convert peer id to public key")
		return false
	}
	// check active members first
	if len(activeNodes.TssMembers) > 0 {
		if common.ExistsIgnoreCase(activeNodes.TssMembers, pubKey) {
			return true
		}
		c.logger.Info().Msgf("active members does not contain %s。", pubKey)
		return false

	}
	// fallback: inactive members
	inactiveNodes, err := c.tssMemberStore.GetInactiveMembers()
	if err != nil {
		c.logger.Error().Err(err).Msg("failed to get inactive members from level db")
		return false
	}
	if len(inactiveNodes.TssMembers) > 0 {
		if common.ExistsIgnoreCase(inactiveNodes.TssMembers, pubKey) {
			return true
		}
		c.logger.Info().Msgf("inactive members does not contain %s。", pubKey)
		return false

	}
	return false
}

func (c *Communication) ExportPeerAddress() map[peer.ID]AddrList {
	peerStore := c.host.Peerstore()
	peers := peerStore.Peers()
	addressBook := make(map[peer.ID]AddrList)
	for _, el := range peers {
		if c.host.ID().String() == el.String() {
			continue
		}
		addrs := peerStore.Addrs(el)
		addressBook[el] = addrs
	}
	return addressBook
}

//todo:. getSubscriber 加锁 + retry loop 有竞态风险
//todo: Broadcast channel 可能阻塞 case msg := <-c.BroadcastMsgChan: buffer 满时会卡死 producer
