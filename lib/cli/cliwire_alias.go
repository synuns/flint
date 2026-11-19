// cliwire_alias.go — compatibility aliases for the contract-layer extraction. The
// Store, dispatch, registry, DSN, error-line, and meta KV surfaces moved to lib/wire are
// re-exported as lowercase var and type aliases so remaining cli files compile without edits. Var aliases
// carry over the test seam-swap convention (lowercase var). Initial registration init (the former
// registry.go) and seam injection are owned by the root.go composition root.
package cli

import (
	"github.com/synuns/flint/lib/wire"
)

// Type aliases — interface implementations and embedding unchanged.
type Store = wire.Store
type Handler = wire.Handler

// Function/var aliases — lowercase calls and seam swaps in consumer files stay compatible.
var (
	Register   = wire.Register
	Lookup     = wire.Lookup
	unregister = wire.Unregister
	usageLine  = wire.UsageLine
	errLine    = wire.ErrLine
	queryOnce  = wire.QueryOnce
	runHelp    = wire.RunHelp
	getMetaKV  = wire.GetMetaKV
	setMetaKV  = wire.SetMetaKV

	// DSN resolution (moved from config.go) — ResolveDSN, ConfigPath, and LoadConfigDSN are
	// aliased under their existing public names.
	dsnFromEnv    = wire.DSNFromEnv
	ResolveDSN    = wire.ResolveDSN
	LoadConfigDSN = wire.LoadConfigDSN
	ConfigPath    = wire.ConfigPath
	defaultDSN    = wire.DefaultDSN

	// Threshold system and session
	// label reads. Lowercase vars remain for residual consumers (tuning, doctor, maintain,
	// read, marker).
	thresholdsKeys   = wire.ThresholdsKeys
	ReadSessionLabel = wire.ReadSessionLabel
)
