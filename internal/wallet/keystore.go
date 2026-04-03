package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/blinq-fi/blinq-mm-bot/internal/secret"
)

const (
	encPrefix    = "enc:"
	saltLen      = 16
	nonceLen     = 12
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32
)

type Keystore struct {
	passphrase string
}

// LoadPassphrase resolves the keystore passphrase from Docker secret,
// environment variable, or interactive terminal prompt (in that order).
func LoadPassphrase() string {
	return secret.Load("keystore_passphrase", "KEYSTORE_PASSPHRASE", "Enter keystore passphrase")
}

func NewKeystore(passphrase string) *Keystore {
	if passphrase == "" {
		return nil
	}
	return &Keystore{passphrase: passphrase}
}

func (ks *Keystore) Enabled() bool {
	return ks != nil && ks.passphrase != ""
}

func (ks *Keystore) Encrypt(plainKeyHex string) (string, error) {
	if !ks.Enabled() {
		return plainKeyHex, nil
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(ks.passphrase), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plainKeyHex), nil)

	// Combine: salt + nonce + ciphertext
	combined := make([]byte, 0, saltLen+nonceLen+len(ciphertext))
	combined = append(combined, salt...)
	combined = append(combined, nonce...)
	combined = append(combined, ciphertext...)

	return encPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

func (ks *Keystore) Decrypt(stored string) (string, error) {
	if !IsEncrypted(stored) {
		return stored, nil
	}

	if !ks.Enabled() {
		return "", fmt.Errorf("encrypted keys found but no KEYSTORE_PASSPHRASE set")
	}

	encoded := strings.TrimPrefix(stored, encPrefix)
	combined, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode encrypted key: %w", err)
	}

	if len(combined) < saltLen+nonceLen {
		return "", fmt.Errorf("encrypted key data too short")
	}

	salt := combined[:saltLen]
	nonce := combined[saltLen : saltLen+nonceLen]
	ciphertext := combined[saltLen+nonceLen:]

	key := argon2.IDKey([]byte(ks.passphrase), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong passphrase?): %w", err)
	}

	result := string(plaintext)
	for i := range plaintext {
		plaintext[i] = 0
	}
	return result, nil
}

func IsEncrypted(stored string) bool {
	return strings.HasPrefix(stored, encPrefix)
}

func IsPlaintextKey(stored string) bool {
	if IsEncrypted(stored) || stored == "" {
		return false
	}
	clean := stored
	if strings.HasPrefix(clean, "0x") || strings.HasPrefix(clean, "0X") {
		clean = clean[2:]
	}
	if len(clean) != 64 {
		return false
	}
	for _, c := range clean {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
