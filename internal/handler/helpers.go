package handler

import (
	"crypto/rand"
	"math/big"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomString(n int) string {
	result := make([]byte, n)
	for i := range result {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[idx.Int64()]
	}
	return string(result)
}
