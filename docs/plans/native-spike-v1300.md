# 네이티브 엔진 L0 스파이크 — v1.30.0 M4 (2026-11-24)

상태: active

v1.30.0 스펙 M4(네이티브 엔진 L0 스파이크)의 결론 노트. 핀 버전
바이너리로 최소 동치성(기동·CRUD·합성 벤치·무결성)을 실증하고
v1.31.0 착수 판정을 내린다. 모든 수치는 **합성 데모 데이터·1회 측정**
이다(PERF §1 중앙값 3회 관례 미적용 — L0 예비 측정 명시). 측정 환경:
darwin arm64·일회용 임시 디렉터리·루프백 리스너 1회 기동, 종료 후
프로세스·디렉터리 정리 완료.

## 핀 매핑

- 이미지 핀: `dolthub/doltgresql@sha256:3a119bb6726a0be283cfda2a73fa9fbdff58df06c0b850f245cd1828f57281a6`
  (ci.yml·내장 compose 현행)
- 태그 역산: Docker Hub 태그 조회로 `1.4.0`과 일치(2026-11-24 확인)
- 바이너리: GitHub `dolthub/doltgresql` 릴리스 `v1.4.0` —
  `doltgresql-darwin-arm64.tar.gz`(47,215,320 bytes)
- sha256: `39ce23c683dc94f4e26599398987d18000de78202904a3466645ccb1f7b039ac`
  — 릴리스에 checksums 자산이 **없어** 다운로드 직후 자체 계산값
  기록(TOFU). v1.31.0 다운로더는 해시 원천 확보를 설계 조건으로 한다.

## 절차·결과

1. **다운로드·검증** — 일회용 디렉터리에 내려받고 sha256 기록(위).
   압축 해제 산출물: `bin/doltgres`(약 153MB)·licenses. `doltgres -version`
   → `Doltgres version 1.4.0`.
2. **기동** — `config.yaml`(listener `127.0.0.1:15499`·behavior 기본)
   + `doltgres -data-dir <임시>` 기동. 관찰 1건: config의 `user`
   섹션(flint/flint-dev)이 초기화에 반영되지 않고 기본
   postgres/password로 시작했다 — v1.31.0 초기 자격증명 설계 과제.
3. **부팅 시간** — 최초 기동 약 1초(로그 시각차), 재기동
   부팅+첫 쿼리 0.25초.
4. **CRUD 왕복** — CREATE DATABASE·CREATE TABLE(serial PK)·INSERT·
   SELECT·UPDATE·DELETE 전부 성공(응답 `PostgreSQL 15.5`).
5. **합성 벤치** — 5,000행 단문 INSERT(`generate_series`) 0.04초·
   집계 SELECT 2건 0.02초.
6. **자원** — 유휴 RSS 약 97MiB·부하 직후 약 109MiB. 데이터
   디렉터리 140KiB(5천행 규모).
7. **무결성** — SIGTERM 정상 종료(exit 0) → 재기동 후 행 수 보존
   (5,001행)·`dolt_log` 2커밋(Initialize·CREATE DATABASE).
8. **정리** — PID 지정 종료·임시 디렉터리 삭제 확인(잔재 0).

## L0 범위 밖(M4 통과 기준 잔여 — v1.31.0 과제)

실DB 테스트 스위트 통과·macOS/Linux 서비스 등록(launchd·systemd
user) 실증·컨테이너↔네이티브 전환(dolt_log 깊이·AS OF 동치)·
구핀→신피 기동 및 롤백·초기 자격증명·부하 후 자원 안정화 관찰.

## 결론

**L0 관찰 통과** — L0 기준(기능·무결성·자원 실측)을 충족했다. 다만
이 통과는 v1.31.0 착수 조건의 **사전 관찰**이며, 스펙 통과 기준
4항(실DB 테스트 스위트 통과·양 OS 서비스 등록 실증·컨테이너↔네이티브
전환 동치·구핀→신피 기동 및 롤백)은 **미충족**이다 — v1.31.0 G2에서
4항 충족 계획을 수립한다(2026-11-24 정정, R121-중10). 단 (a)
업스트림 checksums 부재는 다운로더 해시 원천 설계로, (b) config
`user` 섹션 미반영 관찰은 초기 자격증명 설계로 각각 반영해야 한다.
