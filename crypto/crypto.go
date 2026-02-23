package crypto

import (
	"database/sql/driver"
	"encoding/base64"
	"strings"
)

const (
	storagePrefix    = "ENC:v1:"
	storageDelimiter = ":"
)

// EncryptedPayload is kept for API compatibility (plaintext mode uses Ciphertext as base64)
type EncryptedPayload struct {
	WrappedKey string `json:"wrappedKey"`
	IV         string `json:"iv"`
	Ciphertext string `json:"ciphertext"`
	AAD        string `json:"aad,omitempty"`
	KID        string `json:"kid,omitempty"`
	TS         int64  `json:"ts,omitempty"`
}

type AADData struct {
	UserID    string `json:"userId"`
	SessionID string `json:"sessionId"`
	TS        int64  `json:"ts"`
	Purpose   string `json:"purpose"`
}

// CryptoService in plaintext mode: no RSA, no AES, store/retrieve as plaintext or base64
type CryptoService struct{}

// NewCryptoService always succeeds; uses plaintext/base64 mode (no encryption keys required)
func NewCryptoService() (*CryptoService, error) {
	return &CryptoService{}, nil
}

func (cs *CryptoService) HasDataKey() bool {
	return false
}

func (cs *CryptoService) GetPublicKeyPEM() string {
	return ""
}

// EncryptForStorage returns plaintext as-is (plaintext mode)
func (cs *CryptoService) EncryptForStorage(plaintext string, _ ...string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if isEncryptedStorageValue(plaintext) {
		return plaintext, nil
	}
	return base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
}

// DecryptFromStorage returns value as plaintext (base64 decode if possible, else as-is)
func (cs *CryptoService) DecryptFromStorage(value string, _ ...string) (string, error) {
	if value == "" {
		return "", nil
	}
	if isEncryptedStorageValue(value) {
		return value, nil
	}
	// Plaintext mode: try base64 decode, else return as-is
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		if decoded, err = base64.RawStdEncoding.DecodeString(value); err != nil {
			return value, nil
		}
	}
	return string(decoded), nil
}

func (cs *CryptoService) IsEncryptedStorageValue(value string) bool {
	return isEncryptedStorageValue(value)
}

func isEncryptedStorageValue(value string) bool {
	return strings.HasPrefix(value, storagePrefix)
}

// DecryptPayload in plaintext mode: treat Ciphertext as base64-encoded plaintext
func (cs *CryptoService) DecryptPayload(payload *EncryptedPayload) ([]byte, error) {
	if payload == nil || payload.Ciphertext == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		if decoded, err = base64.RawURLEncoding.DecodeString(payload.Ciphertext); err != nil {
			return []byte(payload.Ciphertext), nil
		}
	}
	return decoded, nil
}

func (cs *CryptoService) DecryptSensitiveData(payload *EncryptedPayload) (string, error) {
	data, err := cs.DecryptPayload(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GenerateKeyPair no-op in plaintext mode (returns empty)
func GenerateKeyPair() (string, string, error) {
	return "", "", nil
}

// GenerateDataKey no-op in plaintext mode (returns empty)
func GenerateDataKey() (string, error) {
	return "", nil
}

// ============================================================================
// EncryptedString - GORM custom type (plaintext mode: base64 encode/decode)
// ============================================================================

var globalCryptoService *CryptoService

func SetGlobalCryptoService(cs *CryptoService) {
	globalCryptoService = cs
}

type EncryptedString string

func (es *EncryptedString) Scan(value interface{}) error {
	if value == nil {
		*es = ""
		return nil
	}
	var str string
	switch v := value.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	default:
		*es = ""
		return nil
	}
	if globalCryptoService != nil && str != "" {
		decrypted, err := globalCryptoService.DecryptFromStorage(str)
		if err == nil {
			*es = EncryptedString(decrypted)
			return nil
		}
	}
	*es = EncryptedString(str)
	return nil
}

func (es EncryptedString) Value() (driver.Value, error) {
	if es == "" {
		return "", nil
	}
	if globalCryptoService != nil {
		encrypted, err := globalCryptoService.EncryptForStorage(string(es))
		if err == nil {
			return encrypted, nil
		}
	}
	return string(es), nil
}

func (es EncryptedString) String() string {
	return string(es)
}
