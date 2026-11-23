package cli

import (
	"github.com/synuns/flint/lib/wire"
)

type Store = wire.Store
type Handler = wire.Handler

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

	dsnFromEnv    = wire.DSNFromEnv
	ResolveDSN    = wire.ResolveDSN
	LoadConfigDSN = wire.LoadConfigDSN
	ConfigPath    = wire.ConfigPath
	defaultDSN    = wire.DefaultDSN

	thresholdsKeys   = wire.ThresholdsKeys
	ReadSessionLabel = wire.ReadSessionLabel
)
