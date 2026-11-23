# BREW — Homebrew 배포 검토 (v1.28.0 R4 · 문서만)

> 상태: **검토 문서 — tap 개설 없음**. 개설은 **사용자 요청 시**(release-scope
> §6 게이트). 본 문서는 goreleaser `brews` 구성안·운영 비용·게이트를
> 정리한다. 설치 안내는 현행 4경로(README)가 그대로다.

## 1. 전제(실측 — .goreleaser.yml 기준)

- 빌드 산출: `flint` 바이너리 1종 — goos darwin·linux / goarch arm64·amd64
  (순수 Go·cgo 무구). brew 지원 플랫폼(darwin/arm64·amd64, linux/arm64·amd64)
  와 전부 겹친다 — 크로스 컴파일 문제 없음.
- 아카이브: `flint_{version}_{os}_{arch}.tar.gz` + `checksums.txt`
  (goreleaser checksum 블록이 이미 생성). `.mcpb`·플러그인 zip은 brew
  입력 아님(extra_files — Release 자산 전용).
- 서명: checksums.txt에 cosign 서명(ADR-0014). brew 경로의 무결성은
  checksums.txt 경유로 확보 — formula 자체는 sha256 블록(hash 곱션)으로
  검증한다.

## 2. 이름 충돌 — homebrew core 불가

homebrew-core에 `flint` formula(FLINT 수론 C 라이브러리, flintlib)가
이미 존재한다(2026-11-24 확인: `Formula/f/flint.rb`). core 투고는 이름
충돌로 불가 — **전용 tap 경로만 유효**:

```
brew install synuns/flint/flint
```

이는 BRAND.md 외부 동향 회수(2026-11-24)의 FLINT 항목 회피책과
동일하다.

## 3. goreleaser `brews` 구성안(제안 — 개설 시 채택)

현재 `.goreleaser.yml`에는 `brews` 블록이 없다. 개설 시 추가하는
구성(실제 산출물 명명과 들어맞게):

```yaml
brews:
  - name: flint
    description: Agent memory on a versioned database — spark, knife, edge.
    homepage: https://github.com/synuns/flint
    license: MIT
    repository:
      owner: synuns
      name: homebrew-flint
      branch: main
      # core 밖 별도 repo → 기본 GITHUB_TOKEN으론 push 불가(공개 repo 쓰기 스코프 PAT)
      token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"
    install: |
      bin.install "flint"
    commit_author:
      name: synuns
      email: 57736547+synuns@users.noreply.github.com
    commit_message: "brew: {{ .Version }}"
```

- 아카이브 id `flint`의 name_template가 os/arch를 포함하므로 기본
  매칭으로 타깃별 tar.gz가 잡힌다 — 수동 매핑 불필요.
- 태그 push → CI 릴리스 잡에서 brews 스텝이 formula 커밋을 tap repo에
  push한다(기존 deploy 잡 뒤에 붙는다).

## 4. tap repo 운영 비용

- **공개 커밋 토큰**: 별도 repo(synuns/homebrew-flint) push용 PAT —
  공개 repo 쓰기 스코프. GitHub Actions 시크릿
  `HOMEBREW_TAP_GITHUB_TOKEN`으로 보관(ADR-0014 키와 별개). 토큰은
  정기 로테이션 대상에 포함.
- **formula 유지**: 릴리스마다 버전·sha256 갱신(goreleaser가 자동
  커밋). 언어 런타임 의존 없음(단일 바이너리)이라 의존 수명 관리는
  없다. `brew audit --online`·`brew test` 통과 유지가 상시 비용.
- **파손 대응**: 탭 formula 결함(다운로드 URL·체크섬 불일치 등) 시
  사용자 설치가 깨진다 — 릴리스 감시(watch-release) 대상에 tap 커밋
  성공 여부를 포함해야 한다.
- **철회 비용**: tap repo 삭제로 즉시 폐쇄 가능(설치 사용자는 병째
  유지 — 마이그레이션 안내 1회 필요).

## 5. 게이트

- **개설은 사용자 요청 시** — 본 문서는 검토만 제공한다(v1.28.0 R4).
- 개설 시 절차(제안): ① tap repo 생성(공개) ② PAT 시크릿 등록
  ③ `.goreleaser.yml` brews 블록 추가(본 문서 §3) ④ 시험 태그로
  formula 커밋·`brew install` 실증 ⑤ README 설치 4경로에 tap 경로
  추가(5경로화) — README 갱신은 별도 태스크.
