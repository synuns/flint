# ADR-0018 — distill 출력 계약: 로컬 폴더 한정 집계 파일, 행 단위 멱등

- 상태: 채택
- 일자: 2026-11-23
- 맥락: v1.22.0 — 엄선 지식의 "인사이트" 추출(distill)이 처음 도입된다.
  출력이 생기는 순간 "어디에 쓰이는가"를 명시하지 않으면 원격 전송·
  볼트 오염·중복 출력의 3가지 사고 경로가 열린다.

## 배경 — 출력이 없던 시절의 암묵적 경계

flint의 모든 산출물은 지금까지 (a) stdout 한 줄 JSON, (b) export의
`--out` 파일, (c) 백업 아카이브 중 하나였다. distill은 "지식의 엄선
요약"이라는 새로운 산출물을 만든다 — 내용이 사용자 판단으로 노트 앱·
레포트에 복사될 수 있으므로, 쓰기 표면이 계약 없이 넓어지면
ADR-0015의 원격 쓰기 금지·ADR-0006의 오염 방어와 충돌한다.

## 결정

1. **출력은 지정 폴더 한정** — `flint distill --out DIR`의 `--out`은
   필수(미지정·빈 값 exit 3). 출력 파일은 그 폴더 안에만 만들어지며,
   distill은 볼트 노트(스캔 대상 마크다운)를 직접 수정하지 않는다.
   폴더가 곧 사용자의 검토 공간이다 — 승인 없이 지식 원천(운영 DB의
   knowledge 행)을 건드리지 않는다는 승인제 원칙의 출력면 확장.
2. **런당 집계 파일 1개** — 행마다 파일을 만들지 않는다. 런당
   `insights-<YYYYMMDD-HHMM>.md` 1개, 행 섹션은 id 안정 정렬.
   동일 이름 파일이 이미 있으면 덮어쓰기 전 stderr 경고 1줄
   (export `--out` 관례 승계).
3. **행 단위 멱등 = distilled_at 스탬프** — 선택 조건은
   `recall_score >= distill_min_score AND distilled_at IS NULL AND
   status='active'`(스코어는 recall_events 파생 count — G2 ruling,
   survived 연동은 v1.25.0 D계측 후 재검토). 출력이 성공한 행만
   `distilled_at=now()`로 스탬프되므로 재실행은 미출력분만 낸다.
   임계값은 threshold `distill_min_score`(기본 1.0) 메타 오버라이드
   + `--min-score` 플래그 우선.
4. **원자성은 단방향** — 파일 쓰기 성공 후에만 스탬프한다. 쓰기
   실패 시 어느 행도 스탬프되지 않으므로 재실행이 같은 행을 다시
   낸다(유실 없음). 반대 방향(스탬프 후 쓰기)은 금지 — 스탬프만
   남아 영영 출력되지 않는 행이 생긴다. 스탬프는 dolt 커밋으로
   영속한다(maintain 쓰기 경로 관례).
5. **살균** — 파일에 들어가는 모든 문자열(topic·summary·conditions·
   tags)은 render.Text(SanitizeText)를 통과한다([flint] 마커·제어
   문자 중립화 — ADR-0006). 사용자가 이 파일을 어디로 복사하든
   오염 마커는 이미 중립화되어 있다.
6. **ADR-0015와의 구분** — distill은 로컬 읽기·로컬 쓰기다. 이
   ADR은 "로컬 폴더 한정"만 다루며, 원격 쓰기 경로 개방 금지
   (ADR-0015 결정 3)는 그대로 유효하다. distill 출력물을 원격
   서비스에 붙여넣는 것은 사용자 행위이며, flint는 전송하지
   않는다.

## 결과

- `flint distill --out DIR [--min-score N]` — 미출력 활성 행 중
  임계 이상만 1파일로, 재실행은 잔여분만.
- 스탬프는 schema v17 knowledge.distilled_at(단일 원천) — 별도
  이력 테이블 없음(additive 원칙).
- 볼트 스캔(vault_files)과 distill 출력은 서로 독립 — distill이
  스캔 대상 폴더에 쓰는 일는 계약상 없다(사용자가 --out으로 볼트
  밖 폴더를 지정하는 것이 전제).
