package cli

import _ "embed"

//go:embed embedded-compose.yml
var embeddedComposeYAML string

func EmbeddedCompose() string {
	return embeddedComposeYAML
}

func PinnedImage() string {
	return "dolthub/doltgresql@sha256:3a119bb6726a0be283cfda2a73fa9fbdff58df06c0b850f245cd1828f57281a6"
}
