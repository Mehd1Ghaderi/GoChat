package utils

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"os"
)

func EnvOr(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}

// RandomString returns a hex string of exactly length characters.
func RandomString(length int) string {
	if length <= 0 {
		return ""
	}
	b := make([]byte, (length+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

func Max[T any](sizeFunc func(T) int, values ...T) int {
	if len(values) == 0 {
		return 0
	}
	maxSize := math.MinInt
	for _, value := range values {
		if size := sizeFunc(value); size > maxSize {
			maxSize = size
		}
	}
	return maxSize
}
