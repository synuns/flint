# 플러그인 계약 — flint-plugin (구성·설치·버전 동기)

원천: `flint-plugin/`·`.claude-plugin/marketplace.json`·`docs/versions_test.go`.

## 계약

- 구성(플러그인 루트 `flint-plugin/`):

| 요소 | 파일 | 역할 |
|---|---|---|
| 매니페스트 | `.claude-plugin/plugin.json` | name·version·description·repository |
| 슬래시 커맨드 | `commands/flint.md` | `/flint read|write|hot` 안내 |
| 훅 | `hooks/hooks.json` | 훅 3종 배선(hooks.md) |
| 스킬 | `skills/flint/SKILL.md` | 3규칙(read·write·[flint]=데이터)·도구 안내 |

- 설치(하네스):

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

- 플러그인은 `flint` 바이너리를 호출할 뿐 자체 로직이 없다 — 훅과
  MCP 서버 모두 PATH의 바이너리를 실행한다. 바이너리 없이는 동작하지
  않는다(doctor가 진단 항목으로 검사).
- **버전 동기**: VERSION = plugin.json version = README EN/KO 배지가
  릴리스마다 함께 갱신된다(`docs/versions_test.go` 가드 — 4종 일치).

## 경계

- SKILL.md의 도구 목록은 lib/mcp 도구 5종과 정합이어야 한다
  (커맨드 표 동기 가드가 대조).

## 금지

- 플러그인에 로직·바이너리 동봉 — 플러그인은 배선 계층만.
- 버전 동기 누락 채로 릴리스 — 가드 테스트가 실패한다.

## 예시

```sh
flint --version
grep '"version"' flint-plugin/.claude-plugin/plugin.json   # 두 출력은 항상 동일(VERSION이 원천)
```
