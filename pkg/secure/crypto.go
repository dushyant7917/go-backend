package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"sync"
)

var (
	encryptKey     []byte
	encryptKeyOnce sync.Once
	encryptKeyErr  error
)

const encryptionKeyEnv = "RAZORPAY_ENCRYPTION_KEY"

// getKey lazily loads and validates the encryption key from environment variables.
// Supported key sizes: 16, 24, 32 bytes (AES-128/192/256).
func getKey() ([]byte, error) {
	encryptKeyOnce.Do(func() {
		keyStr := os.Getenv(encryptionKeyEnv)
		if keyStr == "" {
			encryptKeyErr = errors.New("RAZORPAY_ENCRYPTION_KEY is not set")
			return
		}

		key := []byte(keyStr)
		keyLen := len(key)
		if keyLen != 16 && keyLen != 24 && keyLen != 32 {
			encryptKeyErr = errors.New("RAZORPAY_ENCRYPTION_KEY must be 16, 24, or 32 bytes long")
			return
		}

		encryptKey = key
	})

	return encryptKey, encryptKeyErr
}

// EncryptString encrypts the given plaintext string using AES-GCM and returns a base64-encoded ciphertext.
// If the input is empty, it returns an empty string without error.
func EncryptString(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	key, err := getKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptString decrypts a base64-encoded AES-GCM ciphertext string and returns the plaintext.
// If the input is empty, it returns an empty string without error.
func DecryptString(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	key, err := getKey()
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, cipherData := data[:nonceSize], data[nonceSize:]
	plainBytes, err := gcm.Open(nil, nonce, cipherData, nil)
	if err != nil {
		return "", err
	}

	return string(plainBytes), nil
}
