# flint

> version-controlled memory for AI agents

에이전트의 기억을 버전 관리합니다. Doltgres 기반으로 Claude Code·서브에이전트·
OpenClaw·Hermes 등 모든 에이전트가 공유하는 지식·상태 저장소.

사람의 노트는 Obsidian(흑요석)에, 에이전트의 기억은 flint(부싯돌)에.

## 상태

- v0.4 완료 (2026-10-22) — 전체 내용은 [docs/PLAN.md](docs/PLAN.md)
- v0.5 신뢰성·검색 품질 기획 — 빠른 시작은 [docs/QUICKSTART.md](docs/QUICKSTART.md)

## 구조

```
bin/     # flint CLI (Go 단일 바이너리)
cmd/flint/ # main 진입점
lib/     # 내부 모듈 — store·query·db·cli·backup·sqlfs(SQL embed)
docs/    # 기획서·계약·ADR·플랜
data/    # seed.jsonl(추적) + 런타임(git 무시)
```
