package engine

import (
	"errors"
	"fmt"
)

const NativePinVersion = "1.4.0"

var ErrUnsupportedPlatform = errors.New("engine: unsupported platform for native engine download")

var pinHashes = map[string]string{
	"darwin/arm64": "39ce23c683dc94f4e26599398987d18000de78202904a3466645ccb1f7b039ac",
	"darwin/amd64": "",
	"linux/amd64":  "3868d1293ce6b24680d29c31a8661a913656459e02c3ef8f61cb737908d6d667",
	"linux/arm64":  "",
}

func PinnedHash(goos, goarch string) (string, error) {
	h, ok := pinHashes[goos+"/"+goarch]
	if !ok || h == "" {
		return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, goos, goarch)
	}
	return h, nil
}
