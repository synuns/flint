// Package sqlfs provides embed access to the SQL schema·views.
package sqlfs

import _ "embed"

//go:embed schema.sql
var schemaSQL string

//go:embed views.sql
var viewsSQL string

// SchemaSQL returns the embedded schema.sql contents.
func SchemaSQL() string { return schemaSQL }

// ViewsSQL returns the embedded views.sql contents.
func ViewsSQL() string { return viewsSQL }
