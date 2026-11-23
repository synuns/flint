package sqlfs

import _ "embed"

//go:embed schema.sql
var schemaSQL string

//go:embed views.sql
var viewsSQL string

func SchemaSQL() string { return schemaSQL }

func ViewsSQL() string { return viewsSQL }
