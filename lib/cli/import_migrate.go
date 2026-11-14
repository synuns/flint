// import_migrate.go — v1.3.0 T9: flint import --format jsonl|pam CLI 진입.
// 구현은 lib/knowledge/import_jsonl.go(코어)가 소유 — cli는 계약
// 시그니처(st, args, stdin, stdout) int 진입만 재노출한다(기존
// knowledge_alias.go 관례. 커맨드 라우팅은 knowledge.go RunImportRouted
// 1줄 위임).
package cli

import (
	"context"
	"io"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// RunImportMigrate — `flint import --format jsonl|pam [FILE]` 진입(계약
// 시그니처). 무효 format·파손 jsonl exit 3, 서버다운 exit 2.
func RunImportMigrate(st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	return knowledge.RunImportMigrate(context.Background(), st, args, stdin, stdout)
}

// SetSupersededRef — 계보 참조 복원 위임(v1.3.0 T9. realstore_care.go
// Supersede 위임 관례 — 쓰기는 db.Repo 단일 경로).
func (s *realStore) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	return s.repo.SetSupersededRef(ctx, oldID, newID)
}

// compile guard — db.Repo 메서드 시그니처 드리프트 방지.
var _ interface {
	SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error)
} = (*db.Repo)(nil)
