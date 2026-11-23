package db

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/synuns/flint/lib/wire"
)

type EngineInfo struct {
	Kind    string
	Version string
}

var engineVersionRe = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)

func Handshake(ctx context.Context, st wire.Store) (EngineInfo, error) {
	if st == nil {
		return EngineInfo{}, errors.New("handshake: no store")
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT version()`, nil)
	if err != nil {
		return EngineInfo{}, fmt.Errorf("handshake: version() query failed: %w", err)
	}
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		return EngineInfo{}, errors.New("handshake: empty version() result")
	}
	raw := strings.TrimSpace(fmt.Sprint(rows[0][0]))
	info := EngineInfo{Version: raw}
	if m := engineVersionRe.FindStringSubmatch(raw); m != nil {
		info.Version = m[1]
	}
	if _, derr := wire.QueryOnce(ctx, st, `SELECT 1 FROM `+DoltLogTable+` LIMIT 1`, nil); derr == nil {
		info.Kind = "doltgres"
	} else if strings.Contains(strings.ToLower(raw), "postgresql") {
		info.Kind = "postgres"
	}
	return info, nil
}
