package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

var (
	versionGate    = make(chan struct{}, 1)
	runningVersion string
)

func RunningVersion(ctx context.Context) (string, error) {
	select {
	case versionGate <- struct{}{}:
		defer func() { <-versionGate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if runningVersion != "" {
		return runningVersion, nil
	}
	path, err := executablePath()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	version, err := digestExecutable(ctx, file, 1<<30)
	if err == nil {
		runningVersion = version
	}
	return version, err
}

func digestExecutable(ctx context.Context, source io.Reader, maximum int64) (string, error) {
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	reader := io.LimitReader(source, maximum+1)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := reader.Read(buffer)
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		total += int64(n)
		if total > maximum {
			return "", ErrInventory
		}
		_, _ = hash.Write(buffer[:n])
		if err == io.EOF {
			if total == 0 {
				return "", ErrInventory
			}
			return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
		}
		if err != nil {
			return "", err
		}
	}
}
