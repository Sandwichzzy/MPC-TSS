package p2p

import (
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	maddr "github.com/multiformats/go-multiaddr"
)

type Message struct {
	PeerID  peer.ID
	Payload []byte
}

type AddrList []maddr.Multiaddr

func (al *AddrList) Type() string {
	//TODO implement me
	panic("implement me")
}

func (al *AddrList) String() string {
	addresses := make([]string, len(*al))
	for i, addr := range *al {
		addresses[i] = addr.String()
	}
	return strings.Join(addresses, ",")
}

func (al *AddrList) Set(value string) error {
	addr, err := maddr.NewMultiaddr(value)
	if err != nil {
		return err
	}
	*al = append(*al, addr)
	return nil
}
