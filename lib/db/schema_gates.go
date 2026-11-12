// schema_gates.go — T1(v0.41.0) 승격 게이트 등록표. admin.go의
// EnsureSchemaV* 메서드를 단일 소스로 구성한다 — cli/root.go ensureSchemas의
// 순차 실행과 최신 스키마 버전(LatestSchemaVersion)이 전부 여기서
// 파생된다. 게이트 추가는 admin.go 함수 + 이 등록표 한 줄뿐이며,
// 등록 누락은 TestSchemaGatesContiguous·TestSchemaGatesCoverAdmin이 잡는다
// (함수만 추가하고 호출을 잊던 v0.28.1류 무경고 스킵 폐지).
package db

import "context"

// LatestSchemaVersion — 이 CLI가 아는 최신 스키마 버전(status·doctor
// schema 호환 검사의 상한. 등록표 마지막 원소와 같아야 한다).
const LatestSchemaVersion = 9

// SchemaGate — 버전 승격 게이트 1건. Version은 목표 schema_version,
// Ensure는 그 버전으로 무손실 승격하는 admin.go 게이트(멱등 — 수치
// 비교 단락으로 상위 버전 DB에서는 트랜잭션 없이 no-op).
type SchemaGate struct {
	Version int
	Ensure  func(ctx context.Context, r *Repo) error
}

// SchemaGates — v2부터 LatestSchemaVersion까지 연속 승격 순서(순서 불변:
// v1 DB가 최신으로 건너뛰며 중간 컬럼·인덱스·테이블을 놓치지 않게 한다).
// 메서드 값 참조라 게이트 함수 부재는 컴파일 수준에서 노출된다.
func SchemaGates() []SchemaGate {
	return []SchemaGate{
		{2, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV2(ctx) }}, // sessions.tasks·projects.note 컬럼
		{3, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV3(ctx) }}, // 필터 경로 인덱스 3종(ADR-0008)
		{4, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV4(ctx) }}, // 참조 계측 컬럼(read_count·last_read_at)
		{5, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV5(ctx) }}, // 계보 컬럼(superseded_by)
		{6, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV6(ctx) }}, // 효과 계측 테이블(read_events)
		{7, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV7(ctx) }}, // 계측 정밀화(ids·session_starts·hot_events)
		{8, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV8(ctx) }}, // hit 품질 근사(first_rank)
		{9, func(ctx context.Context, r *Repo) error { return r.EnsureSchemaV9(ctx) }}, // 계측·파생 쿼리 인덱스 4종(ts 3종·pending partial)
	}
}
