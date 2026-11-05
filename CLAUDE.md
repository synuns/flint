# flint 저장소 규칙

## 저장소 목적 (최상위 원칙)

이 저장소는 **flint 오픈소스 패키지만** 담는다. 개인 DB 사용(운영 지식·
프로젝트 기록)과 엄격 분리한다.

- 개인 데이터(실제 프로젝트명·개인 문서 발췌·기기 경로·실명·이메일)를
  어떤 파일에도 넣지 않는다. 스펙·플랜·테스트 픽스처의 예시는 항상
  중성 데모 데이터로만 만든다.
- 데모/시드 데이터는 합성 데이터만(`data/seed.jsonl` 참조).
- 운영 DB의 실제 행(pending·knowledge·hot 출력)을 문서·스펙·커밋에
  인용·복사하지 않는다.
- 런 아카이브(incidents·progress)는 repo에 커밋하지 않는다(.superlite
  로컬 보관).

## 방어 계층

- `make ci` → `scripts/check_personal.py`(범용: 이메일·기기 절대경로)
- 하네스 PreToolUse 훅(`.claude/settings.json` → `scripts/hook_personal_guard.sh`)
  — git commit/push/add 직전 차단
- 개인 고유 마커 목록: `~/.config/flint/personal-markers.txt` (repo 밖)

## 기타

- 커밋 작성자는 GitHub 식별(`synuns <57736547+synuns@users.noreply.github.com>`)
  — 로컬 git config로 고정됨(전역 아님).
- 개발 루프·아키텍처 관례는 docs/PLAN.md·docs/TUNING.md 참조.
