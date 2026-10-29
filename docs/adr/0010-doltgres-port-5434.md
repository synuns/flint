# ADR-0010: Doltgres 호스트 포트 = 5434

- **상태**: 채택 (2026-10-29, v0.9.7 R2) — ADR-0001 대체
- **맥락**: 외부 워크스페이스 컨테이너(branding-doltgres-1)가 호스트 5433을
  점유(2026-10-29 실측). flint 데이터 전체가 해당 컨테이너에 존재해 소실
  위험 상태였고, flint-doltgres-1은 바인딩을 상실한 채 방치됐다. ADR-0001의
  5433 고정 전제가 깨져 정정이 필요했다.
- **결정**: 컨테이너 내부는 5432 유지, 호스트 노출은 **5434**. 접속 문자열
  `postgresql://flint:flint-dev@localhost:5434/flint`. docker-compose·
  Makefile(DSN·FLINT_TEST_DSN)·ci.yml·tests 기본 DSN·QUICKSTART 전부 5434.
- **이전 절차**(커밋 후 코디네이터 운영 작업): ① 신선 backup(`flint
  backup`) ② compose 재구성 — flint-doltgres-1 재생성·5434 바인딩
  (branding 컨테이너은 무손상 유지) ③ `flint migrate` ④ `flint restore
  --file` ⑤ 행수·read 검증. 이전 실패 시 원복 경로(5433) 유지.
- **되돌림 트리거**: 외부 컨테이너가 5433 반납 후 점유 충돌이 재발하지 않는
  것이 확인되면 복귀 검토 가능(접속 문자열 교체 + compose 포트만).
