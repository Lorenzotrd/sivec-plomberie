package wallet

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha512"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
)

// DerivePrivateKey generates a deterministic private key from a mnemonic/seed and an index.
//
// Uses HMAC-SHA512 (the same primitive underlying BIP32 key derivation) for
// proper key stretching. The mnemonic is the HMAC key and "derive:<index>" is
// the message, producing a 64-byte output from which the first 32 bytes are
// used as the private key.
//
// BREAKING CHANGE: This replaces the previous Keccak256(mnemonic:index)
// scheme. Existing wallets already stored in wallets.json are unaffected,
// but regenerating from the same mnemonic will produce different addresses.
func DerivePrivateKey(mnemonic string, index int) (*ecdsa.PrivateKey, string, error) {
	// HMAC-SHA512: key=mnemonic, message="derive:<index>"
	mac := hmac.New(sha512.New, []byte(mnemonic))
	mac.Write([]byte(fmt.Sprintf("derive:%d", index)))
	hash := mac.Sum(nil)

	// Use first 32 bytes as the private key (same as BIP32 master key derivation)
	privateKey, err := crypto.ToECDSA(hash[:32])
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate private key: %w", err)
	}

	return privateKey, PrivateKeyToHex(privateKey), nil
}
