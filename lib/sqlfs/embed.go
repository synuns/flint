// Package sqlfs는 SQL 스키마·뷰의 embed 접근을 제공한다.
package sqlfs

import _ "embed"

//go:embed schema.sql
var schemaSQL string

//go:embed views.sql
var viewsSQL string

// SchemaSQL은 embed된 schema.sql 내용을 반환한다.
func SchemaSQL() string { return schemaSQL }

// ViewsSQL은 embed된 views.sql 내용을 반환한다.
func ViewsSQL() string { return viewsSQL }
