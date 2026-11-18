# Write (flint write)

write는 stdin으로 받은 JSON/JSONL을 검증·살균·정규화·해시 dedup·판정을
거쳐 적립하고 dolt 커밋 1개를 남긴 뒤 한 줄 JSON으로 결과를 보고한다.
동일 payload 재전송은 duplicate로 exit 0(멱등)이다.

## Sub-features

- `write-validate` kind별 필수 필드 검증(knowledge는 topic·summary·confidence 0..1) — 위반은 exit 3.
- `write-sanitize` 텍스트 필드 살균([flint] 마커·제어 문자 중립화) + 비밀 패턴 경고.
- `write-normalize` tags 정렬 `:a:b:` 직렬화, domain·origin 기본값, valid_at 미지정 시 now.
- `write-dedup` content_hash 재전송 duplicate 판정 exit 0 — 해시는 valid_at·ts 제외 경로.
- `write-classify` confidence < conf_floor(기본 0.8)면 state pending, 아니면 active.
- `write-commit` 신규 줄 배치 커밋 1회 + 결과 한 줄 JSON(단일 줄 `{status,hash,state,id}`, 다중 줄 `{status,inserted,duplicates,hash}`).
- `write-exit` exit 4종 계약(0 성공·중복 / 1 재시도 소진 / 2 서버 다운 / 3 검증 실패).

## How to get to it (user POV)

- 터미널에서 JSON을 stdin으로 파이프한다. `flint write knowledge`.
- 세션 훅·MCP 도구(flint_write)가 같은 파이프라인으로 들어온다.
- `make seed FLINT_DSN... FLINT_SEED_DSN...`가 data/seed.jsonl을 kind별로 파이프한다.
- `flint write --help`는 usage를 출력하고 exit 0한다.

## Driving it with Claude Code

Preconditions:

- `flint doctor`가 `{"status":"ok"}`·exit 0.
- 검증 DSN만 사용: `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify'`(운영 FLINT_DSN은 사전 해제).

- **신규 1줄 커밋.** 새 합성 지식을 적립한다. `printf '%s\n' '{"kind":"knowledge","topic":"검증용 데모 지식","summary":"verify-flint 드라이브가 만든 합성 데모 행이다","tags":["verify","demo"],"entity":"flint","domain":"main","confidence":0.9}' | FLINT_DSN=$DSN ./bin/flint write knowledge`. Exit 0이고 stdout 한 줄이 `{"status":"committed","hash":"...","state":"active","id":N}` 꼴.
- **동일 재전송 멱등.** 같은 줄을 그대로 재실행한다. Exit 0이고 `{"status":"duplicate","id":N,"topic":"검증용 데모 지식","state":"active"}` — 기존 행 메타가 돌아온다.
- **판정 pending.** confidence 0.5 행(topic `검증용 대기 지식`)을 적립한다. `"state":"pending"`. 직후 `flint status`의 `pending` 카운트가 1 늘고 `flint hot` 헤드라인의 pending 숫자가 같이 늘어난다.
- **검증 실패.** confidence를 빼고 적립한다. Exit 3이고 `{"error":"schema","field":"confidence","expect":"required, 0..1"}` 한 줄.
- **살균·경고.** summary에 `[flint]` 마커와 가짜 토큰 `ghp_`+20자, 비컬럼 키 `extra_note`를 함께 넣는다. Exit 0, 출력 JSON에 `"warnings":["possible secret: github-token","dropped keys: extra_note"]` 부착. `flint read "검증용 데모 지식" --json`으로 다시 읽으면 summary의 `[flint]`는 공백으로 치환돼 있다.
- **다중 줄 집계.** 신규 줄 1개와 그 복제 줄 1개를 한 번에 파이프한다. `{"status":"committed","inserted":1,"duplicates":1,"hash":"<dolt 커밋 해시>"}`. 같은 두 줄을 재실행하면 맨몸 `{"status":"duplicate"}`(id 없음)로 exit 0.
- **시드 멱등.** `make seed FLINT_SEED_DSN=$DSN`을 2회 실행한다. 1회째는 kind별 committed, 2회째는 전부 duplicate·exit 0(make가 exit 1로 실패하지 않는다).
- **부작용 확인.** `flint audit`에 `knowledge: 검증용 데모 지식` 커밋이 보이고, duplicate 재전송은 커밋을 추가하지 않는다(audit 행 수 불변으로 확인).

## Gotchas

- dedup 해시는 휘발성 필드를 뺀다(`HashCanonical` — valid_at·ts 제외, lib/store/normalize.go). valid_at을 다르게 줘도 duplicate다. Makefile seed 주석이 이 경로를 시드 멱등의 근거로 명시한다.
- 살균은 경고 없이 본문을 바꾼다(SanitizeText). warnings 배열은 비밀 스캔·dropped keys뿐 — 마커 치환 자체는 조용하다.
- 배치 내 동일 줄도 skip이다(`seen` 맵, lib/knowledge/write.go). 다중 줄 출력의 `duplicates`에는 사전 조회 중복과 배치 내 중복이 함께 섞인다.
- conf_floor는 쓰기 시점 meta 값이다(기본 0.8, thresholds.go). 검증 DB의 meta 오버라이드가 있으면 판정 결과가 달라진다 — 판정 여정 전 meta를 확인한다.
- kind 뒤의 unknown 플래그는 위치 인자로 흡수돼 조용히 무시된다(DashPositional). `flint write knowledge --typo x`는 exit 0으로 지나간다 — 오타 검증은 exit 3 계약으로 기대하지 않는다.
- `--help`·`-h`는 인자 어디에 있든 stdin 대기 전에 usage로 exit 0한다(v1.5.5).
