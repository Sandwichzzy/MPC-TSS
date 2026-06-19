package conversion

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

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
