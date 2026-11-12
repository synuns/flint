// import.go — v0.27.0 R3(온보딩 임포트): 기존 규칙 파일(CLAUDE.md·
// AGENTS.md 등)을 pending 지식으로 변환 — 조사 설계 승계(헤딩 단위 분할·
// confidence 0.5 → approve 경로·content_hash dedup). dry-run 기본·
// --commit 확정(maintain --expire-pending 관례 승계).
// v0.39.0 T2: lib/cli/import.go 이동(계약 불변). 커맨드 등록 init은
// knowledge.go로 통합.
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// importCandidates — 발견 순위(조사 확정: Claude Code /import 준용).
var importCandidates = []string{
	"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".github/copilot-instructions.md",
	// .cursorrules 제거(리뷰 5): 평문 불릿 규칙이라 ## 헤딩 분할이 비는
	// 약속 불일치 — 지원 시 불릿 단위 분할 확장과 함께 추가.
}

// importSection — 헤딩 단위 분할 결과 1건.
type importSection struct {
	Heading, Text string
}

// fenceTracker — 코드 펜스 상태기(v0.27.3: ```와 ~~~ 양쪽, CommonMark
// fenced code blocks). 여는 마커와 닫는 마커가 같은 문자여야 닫는다.
type fenceTracker struct {
	open   bool
	marker byte // '`' 또는 '~'
}

// fenceDelim — 라인이 펜스 구분자(동일 문자 3+ 접두)면 그 문자.
func fenceDelim(line string) (byte, bool) {
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return 0, false
	}
	c := t[0]
	if c != '`' && c != '~' {
		return 0, false
	}
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n >= 3 {
		return c, true
	}
	return 0, false
}

// update — 라인으로 펜스 상태 갱신. 반환 true = 이 라인이 구분자(본문
// 아님) — 헤딩 판정·주석 제거에서 제외한다.
func (f *fenceTracker) update(line string) bool {
	c, isDelim := fenceDelim(line)
	if !isDelim {
		return false
	}
	if !f.open {
		f.open = true
		f.marker = c
		return true
	}
	if c == f.marker { // 닫는 펜스(열린 마커와 동일 문자)
		f.open = false
		return true
	}
	return false // 다른 문자 펜스는 열린 펜스 내부의 본문
}

// danglingFenceIdx — EOF까지 닫히지 않고 열려 있는 펜스의 개시 라인
// 인덱스(리뷰 2 — v0.27.3). 미종결 펜스(장식용 ~~~·닫힘 누락)가 나머지
// 문서의 헤딩을 전부 삼키는 무음 지식 유실 방지: 개시 라인을 본문으로
// 되돌리는 중립화 세트를 계산한다. skip은 이미 중립화된 라인.
func danglingFenceIdx(lines []string, skip map[int]bool) int {
	var f fenceTracker
	openIdx := -1
	for i, line := range lines {
		if skip[i] {
			continue
		}
		if _, isDelim := fenceDelim(line); isDelim && !f.open {
			openIdx = i
		}
		f.update(line)
	}
	if f.open {
		return openIdx
	}
	return -1
}

// neutralizeDanglingFences — 미종결 펜스 개시 라인 인덱스 집합. 문서가
// 균형(모든 펜스 닫힘)을 이룰 때까지 반복 계산한다.
func neutralizeDanglingFences(lines []string) map[int]bool {
	skip := map[int]bool{}
	for {
		idx := danglingFenceIdx(lines, skip)
		if idx < 0 {
			return skip
		}
		skip[idx] = true // 이 라인은 펜스 개시가 아니라 본문
	}
}

// parseMarkdownSections — 헤딩 단위 분할(## 기준 — 표준 라이브러리만).
// v0.27.0 리뷰 6: 코드 펜스 내부의 "## "는 헤딩이 아니다.
// v0.27.3: ~~~ 펜스도 인식(CommonMark) — 물결 펜스 내부 헤딩 오판
// 유사 지식 적립 방지. 미종결 펜스는 개시 라인을 본문으로 중립화(나머지
// 문서 헤딩 보존).
func parseMarkdownSections(body string) []importSection {
	lines := strings.Split(body, "\n")
	skip := neutralizeDanglingFences(lines)
	var out []importSection
	var cur importSection
	var fence fenceTracker
	for i, line := range lines {
		isDelim := false
		if !skip[i] {
			isDelim = fence.update(line)
		}
		if isDelim || fence.open {
			cur.Text += line + "\n" // 구분자·펜스 내부는 본문에 보존
			continue
		}
		if strings.HasPrefix(line, "## ") {
			if cur.Heading != "" && strings.TrimSpace(cur.Text) != "" {
				out = append(out, cur)
			}
			cur = importSection{Heading: strings.TrimSpace(line[3:])}
			continue
		}
		cur.Text += line + "\n"
	}
	if cur.Heading != "" && strings.TrimSpace(cur.Text) != "" {
		out = append(out, cur)
	}
	return out
}

// warnSecrets — v0.39.0 T4 수렴 완료: 구현은 done.go 이동분이 단일 소스
// (세션 레코드·세션 지식화·import 3중 복제 수렴 — v0.27.2 리뷰 7).

// RunImport — `flint import [path] [--commit]`. 인자 없음 = 현재
// 디렉터리 후보 탐색. dry-run 요약 기본·--commit 적립 확정.
// v0.27.3: 미확인 플래그는 경로가 아니라 사용법 오류(exit 3) — 오타
// 플래그가 읽기 불가 파일로 조용히 버려지던 사각 제거.
func RunImport(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	commit := false
	var pos []string
	for _, a := range args {
		if a == "--commit" {
			commit = true
		} else if strings.HasPrefix(a, "-") {
			// v0.27.3: 미확인 플래그는 사용법 오류 — 오타 플래그가 읽기 불가
			// 파일로 조용히 버려지던 사각 제거. v0.34.3: 무효 인자 계약
			// exit 3(doctor·tuning·diff 정합 — 주석·코드 불일치 해소).
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("unknown flag: %s", a)))
			return 3
		} else {
			pos = append(pos, a)
		}
	}
	var files []string
	var unreadable []string
	if len(pos) > 0 {
		files = pos
	} else {
		for _, c := range importCandidates {
			if _, err := os.Stat(c); err == nil {
				files = append(files, c)
			}
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, `{"status":"none","detail":"임포트 후보 파일 없음(CLAUDE.md·AGENTS.md 등)"}`)
		return 0
	}
	type item struct{ File, Heading, Summary string }
	var items []item
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			unreadable = append(unreadable, f) // 리뷰 5: 원인 숨김 방지
			continue
		}
		for _, sec := range parseMarkdownSections(stripImportNoise(string(b))) {
			summary := strings.TrimSpace(sec.Text)
			if len([]rune(summary)) < 12 {
				continue
			}
			items = append(items, item{f, sec.Heading, render.TruncateRunes(summary, 500)})
		}
	}
	// v0.27.3: 혼재(읽기 가능+불가)일 때도 읽기 불가 파일을 보고 — 부분
	// 유실이 '전부 완료'로 보이던 오탐 제거. 파일명은 JSON 인코딩(경로
	// 내 따옴표·백슬래시 파손 방지).
	unreadableJSON := "[]"
	if len(unreadable) > 0 {
		if enc, err := json.Marshal(unreadable); err == nil {
			unreadableJSON = string(enc)
		}
	}
	if len(items) == 0 {
		if len(unreadable) > 0 {
			fmt.Fprintln(stdout, `{"status":"error","detail":"읽기 불가","files":`+unreadableJSON+`}`)
			return 1
		}
		fmt.Fprintln(stdout, `{"status":"empty","detail":"변환 가능 섹션 없음(## 헤딩 필요)"}`)
		return 0
	}
	if !commit {
		fmt.Fprintf(stdout, `{"status":"dry-run","files":%d,"sections":%d,"unreadable":%s,"next":"--commit 적립 후 flint approve로 승격"}`+"\n",
			len(files), len(items), unreadableJSON)
		return 0
	}
	// 리뷰 8: dedup 예측 후 단일 배치 커밋(write 경로 패턴 — N트랜잭션 회피).
	// v0.27.3: origin은 filepath.Clean 정규화 — ./CLAUDE.md·a//b.md 등 경로
	// 표기 차이가 다른 해시를 가져 이중 적립되던 멱등 깨짐 수선. 디렉터리가
	// 다른 동일 파일명은 서로 다른 규칙이므로 충돌하지 않는다(basename
	// 정규화의 교차 충돌 — 리뷰 1).
	var batch []map[string]any
	seen := map[string]bool{} // 배치 내 중복(같은 파일 2회 지정 등) 차단
	dedupErrs := 0
	for _, it := range items {
		topic := store.SanitizeText(render.TruncateRunes("규칙["+it.Heading+"]", 80))
		origin := store.SanitizeText("import:" + filepath.Clean(it.File))
		rec := map[string]any{
			"kind":       "knowledge",
			"topic":      topic,
			"summary":    store.SanitizeText(it.Summary),
			"tags":       []string{"process"}, // v0.27.2: 문자열은 normalizeTags에서 소거
			"domain":     "main",
			"confidence": 0.5,
			"status":     "pending",
			"origin":     origin,
		}
		rec = store.Normalize("knowledge", rec)
		rec["content_hash"] = store.ContentHash(store.HashCanonical(rec))
		if h, _ := rec["content_hash"].(string); h != "" {
			if seen[h] {
				continue // v0.27.3: 배치 내 동일 해시(동일 파일 2회 지정 등)
			}
			seen[h] = true
			exists, err := st.ExistsHash(ctx, h)
			if err != nil {
				// v0.27.3: 조회 오류는 존재 아님 처리 금지(write 계약) —
				// 항목 skip+경고로 부분 적립. 전량 실패는 아래에서 실패 보고.
				fmt.Fprintf(os.Stderr, "[flint] import %s: dedup 조회 실패 — skip: %v\n", it.File, err)
				dedupErrs++
				continue
			}
			if exists {
				continue // dedup — 재임포트 멱등
			}
		}
		// v0.27.2: 저장 시점 비밀 스캔 — write 경로 계약 정렬(경고만, doctor
		// 사후 스캔과 이중 방어). dedup 통과분만 경고(리뷰 3 — 영구 skip
		// 항목의 재경고 소음 방지).
		warnSecrets("import "+it.File, it.Summary)
		batch = append(batch, rec)
	}
	if len(batch) == 0 && dedupErrs > 0 {
		// 리뷰 3: 전량 조회 실패는 '전부 중복'이 아니라 장애 — 거짓 성공
		// 금지(write 계약 정렬).
		fmt.Fprintf(stdout, `{"status":"error","detail":"dedup 조회 실패 %d건","unreadable":%s}`+"\n", dedupErrs, unreadableJSON)
		return 1
	}
	if len(batch) == 0 {
		fmt.Fprintln(stdout, `{"status":"imported","inserted":0,"skipped":`+fmt.Sprint(len(items))+`,"unreadable":`+unreadableJSON+`}`)
		return 0
	}
	if _, err := st.InsertBatch(ctx, batch); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if dedupErrs > 0 {
		fmt.Fprintf(stdout, `{"status":"imported","inserted":%d,"skipped":%d,"dedup_errors":%d,"unreadable":%s}`+"\n",
			len(batch), len(items)-len(batch)-dedupErrs, dedupErrs, unreadableJSON)
		return 0
	}
	fmt.Fprintf(stdout, `{"status":"imported","inserted":%d,"skipped":%d,"unreadable":%s}`+"\n",
		len(batch), len(items)-len(batch), unreadableJSON)
	return 0
}

// stripImportNoise — HTML 주석 제거. 코드 펜스 내부는 보존(리뷰 6).
// v0.27.3 재작성 — 재감사 발견(주석-펜스 상태기 오염) 3건 수선:
//
//	① 주석 블록 내부의 ``` 라인이 펜스를 열어 문서 나머지를 통째로
//	   삼키던 결함 — 주석 처리가 펜스보다 먼저, 펜스는 주석 제거 후
//	   남은 라인만 검사한다.
//	② 같은 줄 `<!-- 주석 --> 뒷텍스트`가 줄 끝 --> 아니라서 inComment가
//	   잔류, 이후 문서 전체를 무음 삭제하던 결함 — open/close 동시 처리.
//	③ 줄 중간 인라인 주석 잔존 — `앞 <!-- 비고 --> 뒤`에서 주석 부분만
//	   제거하고 앞뒤 텍스트를 보존한다.
func stripImportNoise(s string) string {
	lines := strings.Split(s, "\n")
	skip := neutralizeDanglingFences(lines)
	var b strings.Builder
	var fence fenceTracker
	inComment := false
	for i, line := range lines {
		if fence.open {
			if !skip[i] {
				fence.update(line) // 닫는 펜스
			}
			b.WriteString(line + "\n") // 펜스 내부는 콘텐츠 — 원문 보존
			continue
		}
		if inComment {
			if i2 := strings.Index(line, "-->"); i2 >= 0 {
				inComment = false
				line = line[i2+3:] // 종결 이후 텍스트는 보존
				if strings.TrimSpace(line) == "" {
					continue
				}
			} else {
				continue // 주석 지속 — 라인 제거
			}
		}
		// 주석 쌍 제거 — 같은 줄에 여러 쌍이면 전부(리뷰 4). 종결 없는
		// 개시는 이후 라인 지속.
		for {
			i2 := strings.Index(line, "<!--")
			if i2 < 0 {
				break
			}
			rest := line[i2+4:]
			if j := strings.Index(rest, "-->"); j >= 0 {
				line = line[:i2] + rest[j+3:]
			} else {
				line = line[:i2]
				inComment = true
				break
			}
		}
		// 펜스 개시 판정은 주석 제거 후의 라인으로(①). 미종결 펜스 개시
		// 라인(skip)은 펜스가 아니라 본문(리뷰 2).
		if !skip[i] {
			fence.update(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
