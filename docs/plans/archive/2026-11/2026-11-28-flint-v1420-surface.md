# v1.42.0 표면·배포 기획 — 서명 재실증·이름 재스윕·관측 이월 (2026-11-28)

상태: active

- **선행**: 리서치 완료(2026-11-28 — 8사실)·scope3 배분 갱신 필요
- **사용자 결정(2026-11-28)**: 기술 항목만 진행 — brew tap·마케팅 제외

## 1. 판정 — 핵심 결정

1. **서명 재실증은 문서화+게이트 편입**: 재실증 자체는 운영 절차(최신 자산
   verify-blob+update 왕복) — 코드 변경 불필요. release_gate.sh에 서명
   검증 신설: **대상=직전 태그 자산**(`gh release download -p 'checksums.txt*'`)·
   **cosign.pub 공백 또는 cosign 미설치 시 명시적 스킵(경고 1줄·차단 아님)**.
   발행 전 게이트 시점에는 이번 버전 .sig 부재(비밀키 Secrets 전속)이므로
   직전 자산 재실증이 차등(감사 1호).
2. **이름 재스윕은 BRAND.md 갱신**: GitHub+brew 2표면 수동 스윕(포맷은
   2026-11-24 표 승계) — 결과를 BRAND.md에 기록·날짜 갱신. 코드 변경 불필요.
3. **관측 이월 재판정은 합성 경로로 실증**: read_events_n ≥ 200 트리거는
   METRICS 문서 룰링(코드 임계값 아님) — **합성 시드 ≥200/<200 두 경로에서
   `flint report`의 `read_events_n`·effect 산출 단언**으로 e2e(격리 원칙).
   실제 판정은 운영 세션 별도.

## 2. 계약(불변)

ADR-0014(서명·비밀키 Secrets)·ADR-0003(이름 스윕 의무)·ADR-0009(flint
이름 확정·재론 금지)·실측은 격리 경로·개인 데이터 불침투·주석 금지·TDD.

## 3. 수용 기준

1. release_gate.sh 서명 검증: 직전 태그 자산 verify-blob green(또는 명시적 스킵).
2. BRAND.md 스윕 갱신(2표면·날짜).
3. read_events_n 트리거 판정 e2e green(합성).
4. 전수 GREEN·release-gate(로컬 make ci 포함) green.

## 4. 비목표

brew tap 개설·마케팅(사용자 요청 시 별도)·실제 스윕 결과 변경(운영
세션 몫)·v1.27.0 자율 적용(gating 유지·별도 착수).
