package assets

import (
	"encoding/hex"
	"errors"
)

var (
	ErrFilesystem = errors.New("unsafe node asset filesystem")
	ErrContent    = errors.New("node asset content mismatch")
	ErrClosed     = errors.New("node asset store is closed")
	ErrBounds     = errors.New("node asset cleanup bounds exceeded")
)

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
