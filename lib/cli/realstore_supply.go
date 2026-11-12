// realstore_supply.go — v0.40.0 T3: ops 3탄(report·report_html·backup·
// sigverify·rules_sync) 이동에 수반하는 realStore 메서드 추출 파일
// (realstore_diag·realstore_care이 이은 같은 패턴). backup.go의 Dump 위임
// (ops dumpStore 계약 — RunBackup 주입 st 우선 덤프 경로, v0.34.3 R14)이
// 여기 옮는다. 물리적 풀은 lib/backup.Dump 내부에서 열고 닫는다(defer).
package cli

import (
	"context"
	"io"

	"github.com/synuns/flint/lib/backup"
)

func (s *realStore) Dump(ctx context.Context, w io.Writer) error {
	return backup.Dump(ctx, dsnFromEnv(), w)
}
