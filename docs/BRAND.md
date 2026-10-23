# flint 브랜드·디자인 지침

2026-10-23 브랜딩 세션 산출물. 아이콘·비주얼 생성 시 이 문서의 철학과
지시사항을 프롬프트로 그대로 사용한다.

## 철학 (이름이 뜻하는 것)

- **한 줄**: 사람의 노트는 Obsidian에, 에이전트의 기억은 flint에.
- flint = 불과 도구를 얻기 위해 두드리는 돌. 세 번의 대응:
  1. **타격spark** — 세션 시작 맥락 주입(`flint hot`)은 부딪쳐 튀는 불씨
  2. **날knife** — 깎여 나온 박편은 도구가 된다. 기억은 다듬어 쓰는 것
  3. **흔적edge** — 모든 타격은 흔적을 남긴다. 모든 쓰기는 커밋(Doltgres 셀 단위 이력)
- 정서: 장인의 정확성, 감사 가능성, 복구 가능성 — 마법이 아니라 세공.

## 디자인 지시사항 (검증된 원칙)

1. **이름-사물 하나만** — flint(돌·박편·날) 자체를 그린다. 기능 상징
   (층·스택·노치 등) 금지 — 전부 거부됐다.
2. **Obsidian급 미니멀리즘** — 단일 피사체, 중앙, 여백. 파셋 헤어라인 최대 1개.
3. **모던 AI 그라디언트, 정제된 톤** — violet→fuchsia→pink. 2026 기준 강한
   3스톤보다 부드러운 페이드가 고급스럽다. 글로우는 희미하게.
4. **액센트는 단 하나** — 작은 앰버 스파크(타격점). "스파크 = 기억 주입".
5. **다크 라운드 스퀘어 컨테이너** — Obsidian과 나란히 놓여도 복제로 안 보이는
   차콜(#1A1726~#14161B). Obsidian 로고 형태 복제 금지.
6. **16px 가독** — 축소 시 실루엣만으로 식별 가능해야 한다. 텍스트 금지.

## 마스터 프롬프트 (이미지 생성용, EN)

```text
App icon for "flint", a version-controlled memory store for AI agents.
Dark charcoal rounded-square container (#1A1726 to #14161B), very subtle
violet glow behind the subject. One centered subject: a knapped flint —
a clean, faceted stone blade with a rounded striking bulb at the top
tapering to a sharp tip, drawn with Obsidian-grade minimalism: crisp
geometric facets, at most one thin hairline facet edge, generous negative
space. Fill the blade with a smooth, refined gradient from soft violet
#A78BFA through fuchsia to warm pink #EC4899 — a modern AI-product
gradient, gentle, not loud. Exactly one accent: a small four-pointed
amber spark (#FBBF24) at the upper-right strike point — the moment a
memory is ignited and injected. Flat design with crisp edges, geometric
precision, must stay legible at 16px. No text, no photorealism, no 3D
bevel, no multiple subjects, no letters.
```

### 플랫폼 파라미터
- Midjourney: `--v 6.1 --style raw --ar 1:1 --no text,photo,3d,bevel,letters`
- Stable Diffusion negative: `text, letters, watermark, photorealistic, 3d render,
  bevel, chrome, noise, multiple subjects, gradient banding, obsidian logo`
- ChatGPT/Gemini: 본문 그대로 + "square 1024×1024, flat vector style"

### 변형 라인 (마스터에서 한 문장씩 교체)
- **Pure**: 스파크 문장 삭제 — 가장 무난
- **Ember**: "spark at the upper-right strike point" → "a small amber ember
  glowing from a hollow near the blade's center" — 절제형
- **Flake**: 마지막에 추가 "a tiny triangular flake detaching from the
  lower-right edge, mid-flight" — 동감형

## 금지 사항 (세션에서 전부 거부됨)

- 손코딩 SVG (실행 품질 미달 — 이미지 생성 또는 디자이너만)
- 다크 크리스탈 Obsidian 복제, 스티커·큐트 이탈, 기능 상징 아이콘
- agent·memory 직접 은유 네이밍 (ADR-0009: 전부 직접 경쟁물 충돌)
