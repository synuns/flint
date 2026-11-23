package knowledge

import "github.com/synuns/flint/lib/wire"

func init() {
	wire.Register("write", RunWrite)
	wire.Register("import", RunImportRouted)
	wire.Register("read", RunRead)
	wire.Register("hot", RunHot)
}
