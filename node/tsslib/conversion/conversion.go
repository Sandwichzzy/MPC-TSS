package conversion

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"

	"github.com/bnb-chain/tss-lib/crypto"
	"github.com/bnb-chain/tss-lib/tss"
	"github.com/btcsuite/btcd/btcec/v2"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	crypto2 "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func GetPeerIDFromSecp256PubKey(pk []byte) (peer.ID, error) {
	if len(pk) == 0 {
		return "", errors.New("empty public key raw bytes")
	}
	ppk, err := crypto2.UnmarshalSecp256k1PublicKey(pk)
	if err != nil {
		return "", fmt.Errorf("fail to convert pubkey to the crypto pubkey used in libp2p: %w", err)
	}

	return peer.IDFromPublicKey(ppk)
}

func GetTssPubKey(pubKeyPoint *crypto.ECPoint) (string, []byte, []byte, error) {
	if pubKeyPoint == nil || pubKeyPoint.X() == nil || pubKeyPoint.Y() == nil {
		return "", nil, nil, errors.New("nil pubkey point")
	}
	xBytes := pubKeyPoint.X().Bytes()
	yBytes := pubKeyPoint.Y().Bytes()
	// 补齐 32 bytes（很重要！否则经常出 bug）
	xBytes32 := make([]byte, 32)
	yBytes32 := make([]byte, 32)

	copy(xBytes32[32-len(xBytes):], xBytes)
	copy(yBytes32[32-len(yBytes):], yBytes)
	pubBytes := make([]byte, 0, 65)
	pubBytes = append(pubBytes, 0x04) // uncompressed prefix
	pubBytes = append(pubBytes, xBytes32...)
	pubBytes = append(pubBytes, yBytes32...)

	pubKey, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		return "", nil, nil, fmt.Errorf("invalid secp256k1 pubkey: %w", err)
	}

	pubKeyStr := hex.EncodeToString(pubKey.SerializeCompressed())
	uncompressed := pubKey.SerializeUncompressed()[1:]
	address := ethcrypto.PubkeyToAddress(*pubKey.ToECDSA()).Bytes()

	return pubKeyStr, address, uncompressed, nil
}

func AccPubKeysFromPartyIDs(partyIDs []string, partyIDMap map[string]*tss.PartyID) ([]string, error) {
	pubKeys := make([]string, 0)
	for _, partyID := range partyIDs {
		blameParty, ok := partyIDMap[partyID]
		if !ok {
			return nil, errors.New("cannot find the blame party")
		}
		blamedPubKey, err := PartyIDtoPubKey(blameParty)
		if err != nil {
			return nil, err
		}
		pubKeys = append(pubKeys, blamedPubKey)
	}
	return pubKeys, nil
}

func PartyIDtoPubKey(party *tss.PartyID) (string, error) {
	if party == nil || !party.ValidateBasic() {
		return "", errors.New("invalid party")
	}
	partyKeyBytes := party.GetKey()
	pubKey := hex.EncodeToString(partyKeyBytes)

	return pubKey, nil
}

func BytesToHashString(msg []byte) (string, error) {
	h := sha256.New()
	_, err := h.Write(msg)
	if err != nil {
		return "", fmt.Errorf("fail to caculate sha256 hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func GetParties(keys []string, localPartyKey string) ([]*tss.PartyID, *tss.PartyID, error) {
	var localPartyID *tss.PartyID
	var unSortedPartiesID []*tss.PartyID
	sort.Strings(keys)
	for idx, item := range keys {
		pkBytes, err := hex.DecodeString(item)
		if err != nil {
			return nil, nil, fmt.Errorf("fail to get account pub key (%s): %w", item, err)
		}
		key := new(big.Int).SetBytes(pkBytes)
		// Note: The `id` and `moniker` fields are for convenience to allow you to easily track participants.
		// The `id` should be a unique string representing this party in the network and `moniker` can be anything (even left blank).
		// The `uniqueKey` is a unique identifying key for this peer (such as its p2p public key) as a big.Int.
		partyID := tss.NewPartyID(strconv.Itoa(idx), "", key)
		if item == localPartyKey {
			localPartyID = partyID
		}
		unSortedPartiesID = append(unSortedPartiesID, partyID)
	}
	if localPartyID == nil {
		return nil, nil, errors.New("local party is not in the list")
	}
	partiesID := tss.SortPartyIDs(unSortedPartiesID)
	return partiesID, localPartyID, nil
}

func SetupPartyIDMap(partiesID []*tss.PartyID) map[string]*tss.PartyID {
	partyIDMap := make(map[string]*tss.PartyID)
	for _, id := range partiesID {
		partyIDMap[id.Id] = id
	}
	return partyIDMap
}

func GeneratePartyIDtoP2PIDMaps(parties map[string]*tss.PartyID) (map[string]peer.ID, error) {
	partyIDtoP2PID := make(map[string]peer.ID)
	for id, party := range parties {
		peerID, err := GetPeerIDFromPartyID(party)
		if err != nil {
			return nil, err
		}
		partyIDtoP2PID[id] = peerID
	}
	return partyIDtoP2PID, nil
}

func GetPeerIDFromPartyID(partyID *tss.PartyID) (peer.ID, error) {
	if partyID == nil || !partyID.ValidateBasic() {
		return "", errors.New("invalid partyID")
	}
	pkBytes := partyID.KeyInt().Bytes()
	return GetPeerIDFromSecp256PubKey(pkBytes)
}

func GetPeersID(partyIDtoP2PID map[string]peer.ID, localPeerID string) []peer.ID {
	if partyIDtoP2PID == nil {
		return nil
	}
	peerIDs := make([]peer.ID, 0, len(partyIDtoP2PID)-1)
	for _, value := range partyIDtoP2PID {
		if value.String() == localPeerID {
			continue
		}
		peerIDs = append(peerIDs, value)
	}
	return peerIDs
}
