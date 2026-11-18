# Export (flint export)

export는 저장된 지식을 5포맷(jsonl·md·txt·csv·pam)으로 내보낸다.
본문은 stdout 또는 `--out` 파일로 나가고, 파일 기록 시 `.sha256`
사이드카를 함께 쓴다. 요약·경고는 stderr 1행씩이라 stdout이 항상
본문만 담는다.

## Sub-features

- `export-jsonl` 행당 1 JSON 객체(전 필드 스냅샷) — 재이식용 완전 왕복.
- `export-md` 노트앱 이식용 Markdown(`# flint export` 헤더 + `## #<id> <topic>` 섹션).
- `export-txt` grep용 플레인 텍스트(`==== #<id> <topic> ====` 구분선).
- `export-csv` 11열 헤더 CSV(content 제외, tags `|` 결합, 수식 인젝션 중립화).
- `export-pam` PAM v1.0 memory-store 문서(memories·relations·integrity — pending 행 제외).
- `export-out` `--out FILE` 기록 + `.sha256` 사이드카(sha256sum -c 호환). 기본·`--out -`는 stdout이며 사이드카 없음.
- `export-filter` `--status active|all`·`--tag X`(정확 태그 매칭)·`--since RFC3339`.
- `export-guard` 무효 format·status·since·`--sign` 비pam 조합은 exit 3. 행수 상한 export_import_max_rows(기본 100000) 초과도 exit 3.

## How to get to it (user POV)

- 터미널에서 포맷·출력 위치를 고른다. `flint export --format md --out notes.md`.
- 노트앱 이식(md)·LLM 주입 용사본(txt)·표 분석(csv)·PAM 생태계 교환(pam)·백업 왕복(jsonl).
- 터미널에서 무인자 실행 시 TUI 폼이 열린다(적응형 진입 — 파이프·플래그 경로는 불변).

## Driving it with Claude Code

Preconditions:

- 검증 DSN: `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify'`.
- 시드 적립 완료. 드라이브는 항상 플래그를 붙인다(무인자 TTY 실행 방지 — 파이프 리다이렉트면 안전).

- **jsonl.** `FLINT_DSN=$DSN ./bin/flint export --format jsonl --out /tmp/verify-export.jsonl`. Exit 0, 파일은 행당 1 JSON(`"topic"`,`"content"`,`"status"` 키), stderr에 `exported N rows (jsonl, M bytes)` 1행.
- **md.** `--format md --out /tmp/verify-export.md`. 첫 줄 `# flint export`, 항목 섹션 `## #<id> <topic>`과 메타 라인(`status: active · confidence: ...`).
- **txt.** `--format txt --out /tmp/verify-export.txt`. 항목 구분선 `==== #<id> <topic> ====` 뒤 본문.
- **csv.** `--format csv --out /tmp/verify-export.csv`. 헤더 11열(`id,topic,kind,status,confidence,tags,source,created_at,updated_at,superseded_by,valid_at`) — content 열은 없다.
- **pam.** `--format pam --out /tmp/verify-export.pam.json`. `"memories"`,`"relations"`,`"integrity"` 블록 포함, stderr 요약의 N은 발행 수다(pending 제외).
- **사이드카.** 각 `--out` 파일마다 `<file>.sha256`이 생긴다. `cd /tmp && shasum -a 256 -c verify-export.jsonl.sha256`(Linux는 `sha256sum -c`)이 `OK`를 낸다.
- **stdout·`--out -`.** `FLINT_DSN=$DSN ./bin/flint export --out - > /tmp/verify-stdout.jsonl`. 본문은 stdout으로 나가고 요약은 stderr, 사이드카·이름 `-`의 파일은 생기지 않는다.
- **status all.** `--status all --format jsonl`과 기본(active)의 행수를 비교한다. all이 pending(및 invalidated) 행만큼 크다. 같은 데이터의 `--format pam --status all` 요약 N은 pending을 빼므로 jsonl N보다 작거나 같다.
- **무효 인자.** `--format xml`은 exit 3, `{"error":"schema","field":"format","expect":"jsonl|md|txt|csv|pam"}` 한 줄. `--format jsonl --sign`도 exit 3(`sign` 필드 오류).

## Gotchas

- md 헤더의 `created:`·pam의 `export_id`·`export_date`는 매 실행마다 달라진다. 포맷 간 바이트 동일성 재실행 비교는 jsonl·csv만 성립한다.
- stderr 요약의 행수 기준이 포맷별로 다르다 — pam은 발행 수(pending 제외), 나머지 4포맷은 전행 수(lib/cli/export.go 포맷터 seam 주석).
- 기존 파일 덮어씀 경고 `export: overwriting existing file: <path>`는 stderr에 먼저 나온다. stdout만 보면 경고를 놓친다.
- 검증 DB는 재사용되므로 행수는 여정 누적으로 자란다 — 절대 행수 단언 대신 active/all 차이나 `--tag verify`(write 여정의 합성 태그)로 격리해 단언한다.
- `FLINT_NO_TUI`를 설정하지 않은 터미널 무인자 실행은 TUI 폼으로 분기한다(적응형 진입). CI·파이프가 아니면 플래그를 항상 붙인다.
