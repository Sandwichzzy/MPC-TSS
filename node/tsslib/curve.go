package tsslib

import (
	"github.com/bnb-chain/tss-lib/tss"
	"github.com/btcsuite/btcd/btcec/v2"
)

func init() {
	// tss-lib v1.5 registers the legacy btcec curve type by default.
	// This project uses btcec/v2 for keygen/keysign parameters, so register
	// that concrete curve type as secp256k1 as well for ECPoint JSON encoding.
	tss.RegisterCurve(tss.Secp256k1, btcec.S256())
}
