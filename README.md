# flint

> version-controlled memory for AI agents

에이전트의 기억을 버전 관리합니다. Doltgres 기반으로 Claude Code·서브에이전트·
OpenClaw·Hermes 등 모든 에이전트가 공유하는 지식·상태 저장소.

사람의 노트는 Obsidian(흑요석)에, 에이전트의 기억은 flint(부싯돌)에.

## 상태

- 기획 완결 (2026-10-20) — 전체 내용은 [docs/PLAN.md](docs/PLAN.md)
- v0.1 착수 전 — 기술 검증 3건부터 시작

## 구조

```
bin/     # flint CLI (Go 단일 바이너리)
lib/     # 내부 모듈 (Go 패키지)
sql/     # schema.sql, views.sql, queries/*.sql
docs/    # 기획서·계약 문서
data/    # Doltgres data-dir (git 무시)
```
