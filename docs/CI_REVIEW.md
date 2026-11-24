# flint CI 설계 리뷰 — v0.17.0 (2026-11-06)

대상: `.github/workflows/ci.yml`(v0.16.7 주석 기준 — 현행 워킹트리, v0.17.0 T1의 mcp-publisher v1.8.7 고정 포함).

## 1. 현재 구조 다이어그램

```
pull_request ────▶ test ──통과──▶ 종료(PR은 배포 없음)
main push ────────▶ test ──통과──▶ deploy
                                    ├ VERSION 미변경·태그 존재 → deploy=false 스킵
                                    ├ 신버전 → 태그 생성·push(v*.*.*)
                                    ├ goreleaser release --clean (릴리스 부재 시만)
                                    └ MCP Registry 발행 (항상 — 멱등 재시도)
tag push(v*) ─────▶ test ──통과──▶ deploy(무조건 릴리스 — 수동 폴백)
workflow_dispatch ▶ test (deploy는 push에서만)
```

## 2. 잡별 역할·트리거·권한

| 잡 | 트리거 | 역할 | 권한 |
|---|---|---|---|
| `test` | PR·push·dispatch 전부 | doltgres 서비스(SHA 고정, 5434) 기동 → `doctor --check db` 준비 대기 → `flint_test` DB 생성 → `make ci`(YAML 검증·개인정보 검사·`go test ./... -p 1`) → `make e2e`(실DB e2e) | 미지정(기본 읽기 전용) |
| `deploy` | `needs: test` + `if: push` | VERSION 파일 변경 감지 → 태그 생성·push → goreleaser 릴리스 → mcp-publisher(github-oidc)로 MCP Registry 발행. 태그 push는 수동 폴백 | `contents: write`, `id-token: write`(잡 레벨로 국한) |

## 3. 잘 된 점

1. **이미지 SHA 고정** — doltgres를 태그가 아닌 digests(`@sha256:3a119bb…`)로 고정해 CI 재현성 확보.
2. **권한 최소화가 잡 레벨** — 쓰기 권한(`contents`·`id-token`)이 deploy 잡에만 걸려 있고 test는 기본 읽기 권한. OIDC 발행에 필요한 최소 범위.
3. **멱등 배포 설계** — 태그 존재 감지로 중복 배포 차단, goreleaser는 릴리스 존재 시 스킵, MCP 발행은 항상 실행해 이전 실패 재시도. 재실행이 안전하다.
4. **VERSION 파일 = 배포 단일 진실 원천** — 사용자는 PR에 VERSION 갱신만 하면 태그·릴리스·레지스트리 발행이 전부 자동. 수동 태그 push 폴백도 유지.
5. **타임아웃·직렬화 명시** — 잡별 `timeout-minutes`(20/15)로 매달림 방지, `go test -p 1`로 실DB 테스트 경합 차단, e2e는 `-tags e2e`로 분리.

## 4. 개선 후보 (실행 가능 수준)

반영 이력: **mcp-publisher 버전 고정**(latest → v1.8.7)은 v0.17.0 T1에서 ci.yml에 적용 완료 — 이제 상류 breaking·재현성 위험이 제거됐다(상향 시 수동 실증 후 버전만 갱신).

1. **태그 push 실패 무시 제거** — `git push origin "$TAG" || true`가 실패를 삼키면 원격 태그 없는 릴리스 URL이 깨질 수 있다. `|| true` 제거해 실패 시 잡을 중단.
2. **동시 실행 취소** — `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }` 추가로 같은 PR의 연속 push 실행을 취소해 러너 절약(deploy는 멱등 가드가 있어 안전).
3. **DB 대기 루프 병합** — `doctor --check db` 대기와 psql `SELECT 1` 대기가 사실상 동일한 준비 확인. psql 루프를 doctor 통과 직후 1회 시행으로 줄여 단계·시간 절감.
4. **PyYAML 설치 캐시** — 매 실행 `pip install pyyaml`. `actions/setup-python` + `cache: pip` 도입으로 설치 단계 단축.

## 5. 결론

현재 방향이 맞다. "VERSION 파일 변경 → 태그 → 릴리스 → MCP 발행"의 완전 자동화와 멱등 가드, 잡 레벨 권한 최소화는 소규모 단일 관리자 저장소에 적합한 설계다. 개선은 위 5건 모두 국소적 보강(재현성 고정·실패 처리·자원 절약)이며 구조 변경은 불필요하다.

## 6. 면책 행 관례 — v1.35.1 (구현 정합)

위반 원칙: 검사(vet·lint·errcheck·SDK import 게이트 등)의 위반은
**면책 행 없으면 무조건 fail**이다. 면책 행은 예외가 아니라 기한부
기록이다. 본 절은 v1.35.0의 선기술 정의를 실제 구현
(`scripts/check_llm_imports.py`)에 맞춘 것이다.

1. **면책 행 형식(구현)** — 위반 import **바로 위 줄**의 한 줄 주석:
   `// flint-waiver: <이유> expires=YYYY-MM-DD`
   - 이유: 필수·빈 값 불가 — 왜 지금 고칠 수 없는지 한 문장("등록표 외부
     의존 대기" 등 모호 표현 금지)
   - 만료일: 필수. `expires=` 누락·형식 불량(YYYY-MM-DD 외)·도과 시
     면책 행은 무효 — 해당 위반은 다시 fail이며, 만료 전 재발행(갱신)은
     이유 재검증을 요구한다.
   - 적용 범위: 면책 행 바로 아래 1행의 import(단일 `import` 문 또는
     import 블록 내 `"path"` 행)에만 유효 — 떨어진 행에는 적용되지
     않는다.
2. **면책 없는 위반 = fail** — 구조화 5줄 출력(파일:행·import·이유·
   수정처·요약)과 함께 exit 3. 면책 행이 불량(이유 누락·만료일 누락·
   만료 경과)이면 이유 행에 불량 사유가 표기된다.
3. **주 통로는 주석 면책, env 화이트리스트는 테스트 심** — CI의 유일한
   면책 통로는 위 주석 면책 행이다(`check_llm_imports.py`가 파싱·만료
   검사). `FLINT_LLM_IMPORT_WHITELIST`(JSON 파일)는 CI 통로가 아니라
   `TestLLMImportGate` 계열의 계약 시험 심(seam)이다.
4. **문서 정합** — 면책 행 형식 자체도 게이트(`TestLLMImportGate` 계열:
   유효·만료·이유 누락·만료일 누락·비인접)로 검증한다.
5. **경계** — 면책 행은 기한부 기술 부채의 기록일 뿐 금지 원칙 자체를
   약화하지 않는다(만료 도과 = 원칙 위반 복귀). 제품 코드 LLM 유입은
   여전히 기본 금지이며, 주석 면책은 기한을 명시한 유일한 예외 통로다.

