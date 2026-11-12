// meta.go — v0.34.3 T2(스펙 §1.2 / R12) 계약층 이동(v0.38.0 §1): meta
// 테이블 키-값 접근의 단일 진입점. metrics metaKV·tuning set·setup 노출
// 기록·hot 조회·read 폴백 증분에 복제되던 SELECT / INSERT…ON CONFLICT
// 리터럴을 이 파일로 수렴시킨다(출력·exit 계약 불변).
package wire

import (
	"context"
	"fmt"
)

// GetMetaKV — meta 단일 키 조회. 키 부재·빈 값·전송 오류는 전부
// ("", false)로 귀결된다(오류 구분이 필요한 호출부는 GetMetaKVErr).
func GetMetaKV(ctx context.Context, st Store, key string) (string, bool) {
	v, ok, _ := GetMetaKVErr(ctx, st, key)
	return v, ok
}

// GetMetaKVErr — GetMetaKV의 원형: (값, 존재, 전송 오류)를 그대로
// 돌려준다. 계측 경로(Bump*)는 오류 시 쓰기를 생략해 기존 값을
// 와이프하지 않으므로 err 구분이 계약이다.
func GetMetaKVErr(ctx context.Context, st Store, key string) (string, bool, error) {
	rows, err := QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = $1`, []any{key})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return "", false, err
	}
	return fmt.Sprint(rows[0][0]), true, nil
}

// SetMetaKV — ON CONFLICT 원자 upsert 단일 구현(기존 5곳 복제 수렴).
func SetMetaKV(ctx context.Context, st Store, key, value string) error {
	_, err := QueryOnce(ctx, st,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, []any{key, value})
	return err
}

// CompareSetMetaKV — 원자 조건부 갱신(CAS): 현재 값이 old면 new로
// 바꾸고 true, 아니면 false(v1.0.10 감사 A4 — 병렬 세션의 자원 선점).
// 단일 upsert-WHERE 문으로 부재(최초)·기존값 일치 양쪽을 원자 처리한다.
// BumpMetaKV와 동일 계약: 조건부 쓰기는 모호 실패에서 재실행해도 안전
// 하지 않으므로 재시도 없이 단발 st.Query로 실행한다.
func CompareSetMetaKV(ctx context.Context, st Store, key, oldVal, newVal string) (bool, error) {
	// v1.0.11(R12-A1): 인자는 스프레드 전달 — variadic Query에 슬라이스
	// 1개로 넘기면 pgx가 "expected 3, got 1"로 거부해 CAS가 전면 불능
	// 되었다(실DB 실증 — fake claim 주입 테스트는 이 결함을 못 본다).
	rows, err := st.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $3)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value
		 WHERE meta.value = $2
		 RETURNING key`, key, oldVal, newVal)
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

// BumpMetaKV — 카운터 키 1회 원자 증분. SELECT→INSERT 비원자 왕복
// 없이 단일 upsert에서 계산한다. 기존값이 양의 정수 표기가 아니면
// (부재·파손) 1로 리셋 — 기존 read_fallback_calls 폴백과 동일 결과.
// 계약(v1.0.1 F2): 증분은 비멱등이라 재시도 금지 — QueryOnce(오류 시 1회
// 재실행)를 쓰면 모호 실패(서버에서 실행된 뒤 응답만 유실)에서 이중
// 계상한다. 단발 st.Query로 실행하고 실패는 그대로 전파한다(호출부는
// 계측 원칙으로 흡수 — done.go·read.go 참조). SetMetaKV(멱등 갱신)·
// GetMetaKVErr(읽기)만 QueryOnce를 쓴다.
func BumpMetaKV(ctx context.Context, st Store, key string) error {
	_, err := st.Query(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '1')
		 ON CONFLICT (key) DO UPDATE SET value = CASE
		   WHEN meta.value ~ '^[0-9]+$' THEN (meta.value::bigint + 1)::text
		   ELSE '1' END`, key)
	return err
}
