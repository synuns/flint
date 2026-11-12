# CLI 계약 — `flint <command> [args]`

원천: `lib/wire/registry.go`·`lib/wire/run.go`·`lib/store/pipeline.go`(MapExit).

## 계약

- 서브커맨드는 `wire.Register`로 등록한다(구현 패키지 init — 개방-폐쇄).
- 사람 노출 커맨드 목록의 단일 소스는 `lib/wire/registry.go`의
  `usageOrder` 슬라이스다. usage 문장(`flint` 무인자·`flint help`)은
  이 순서로 조립된다.
- 출력은 stdout에 한 줄 JSON. 에러도 JSON(`error` 키 — 구조화,
  원시 결합 금지).
- exit 코드 4종(`store.MapExit`):

| 코드 | 의미 |
|---|---|
| 0 | 성공(중복 duplicate 포함) |
| 1 | 일반 오류 — 과적재 코드: 재시도 소진(retries_exhausted)·unknown command·usage·`doctor` fail(fail 1건 이상)·제로히트 침묵(eligible 제로히트는 무출력 exit 1 — read의 정상 결과) |
| 2 | 서버 다운(server_down) |
| 3 | 무효 인자·검증 실패(`SchemaError` — 필드 단위 검증: 무효 `--status`·`--limit`, 값 없는 값형 플래그 등) |

- exit 1 과적재 확인: `read` 제로히트 침묵은 무출력, 나머지 exit 1은
  errLine(JSON) 1줄 동반 — MCP 도구 경로(`flint mcp`)는 이 출력 유무로
  `isError`를 판정한다(빈 출력 exit 1 = 정상, 출력 있는 exit 1 = 오류).
- 스키마 버전 불일치(meta schema_version ↔ `db.LatestSchemaVersion`)는
  exit 3이 아니다 — `doctor` schema 검사 fail(exit 1)과 `setup`
  마이그레이션(승격 게이트 등록표)으로 표면화된다.

- store-free fast path: `version`·`teardown`·`mcp`·`update`·`help`는
  DB 기동 없이 즉시 응답. `setup`은 DB 다운이 주 시나리오라 연결
  실패 시에만 store 없이 진행한다.
- 훅 전용 진입점 `session-start`·`session-end`는 등록되지만
  usage에 노출하지 않는다(`hiddenFromUsage`).

## 경계

- 훅 진입 커맨드의 비간섭: `compact`은 DB 부재 시 조용히 exit 0,
  `nudge`는 에러 출력 없이 exit 코드로만 종료(서버 다운=2).

## 금지

- `Register`만 하고 `usageOrder`에 추가하지 않기(사람에게 보이지
  않는 커맨드가 됨 — `TestUsageOrderMatchesRegistry`가 드리프트를
  잡는다).
- usage 문장에 커맨드명을 하드코딩한 리터럴 추가 — 항상
  `usageOrder`에서 조립한다.

## 예시

```sh
flint read "마이그레이션 결정"
# → 행별 "id|topic|summary" 텍스트(기본 필드, 1행 1지식), exit 0
flint read "마이그레이션 결정" --json
# → 행별 JSONL({"id":...,"summary":...,"topic":...}), exit 0
flint nosuchcmd
# → {"error":"unknown command: nosuchcmd"}, exit 1
```
