package i18n

import (
	"os"
	"strings"
)

var en = map[string]string{
	"usage.line":                 `{"usage":"flint <%s> [args]"}`,
	"usage.line.plain":           "flint <%s> [args]",
	"usage.approve":              "usage: flint approve --id N [--supersede OLD]",
	"usage.audit":                "usage: flint audit [--limit N] [--session ID] [--stat] [--telemetry]",
	"usage.backup":               "usage: flint backup [--out FILE] [--encrypt] [--mask]",
	"usage.brief":                "usage: flint brief [query] [--limit N]",
	"usage.compact":              "usage: flint compact (hook entry — no flags)",
	"usage.diff":                 "usage: flint diff <from> <to> [--summary] [--data|--schema]",
	"usage.distill":              "usage: flint distill --out DIR [--min-score N]",
	"usage.doctor":               "usage: flint doctor [--list-checks] [--check NAME] [--fix] [--json]",
	"usage.done":                 "usage: flint done --session <id>",
	"usage.export":               "usage: flint export [--format jsonl|md|txt|csv|pam] [--out FILE] [--status active|all] [--tag X] [--since RFC3339] [--sign (pam only)]",
	"usage.hot":                  "usage: flint hot [--session ID] [--full]",
	"usage.import":               "usage: flint import [path] [--commit] | flint import --format jsonl|pam [FILE]",
	"usage.import.format":        "usage: flint import --format jsonl|pam [FILE]",
	"usage.index":                "usage: flint index [--vault PATH] [--limit N]",
	"usage.maintain":             "usage: flint maintain --check | --dup-suspects [--limit M] | --conflict-suspects [--limit M] | --stale [--days N] [--limit M] | --invalidate-stale [--yes] [--days N] [--limit M] | --invalidate (--id N --reason S | --ids 1,2,3 --yes) | --merge --from N --to M | --supersede OLD --by NEW | --expire-pending N [--commit] | --meta-audit | --prune-invalidated [N] [--commit] | --decay-audit [--limit M] | --age-audit [--limit M] | --merge-audit [--limit M] | --recall-audit [--limit M] | --reaffirm (--id N | --ids 1,2) --yes | --recheck N [--conditions S --yes] | --gc [--shallow] --yes | --purge-dropped [--yes] | --tui (opt-in, run alone)",
	"usage.nudge":                "usage: flint nudge [--clear]",
	"usage.pending":              "usage: flint pending [--tui]",
	"usage.pending.json":         "usage: flint pending [--json]",
	"usage.profile":              "usage: flint profile [--apply normal|minimal]",
	"usage.read":                 "usage: flint read [query] [--json] [--fields F] [--limit N] [--status S] [--as-of T] [--session ID] [--source X] [--tag T] [--entity E] [--domain D] [--min-confidence N] [--origin agent|human] [--related N] [--why]",
	"usage.report":               "usage: flint report [--tui] | [--days N] [--html] [--out FILE] [--text]",
	"usage.report.args":          "usage: flint report [--days N] [--html] [--out FILE] [--text]",
	"usage.restore":              "usage: flint restore --file FILE [--drill]",
	"usage.seed":                 "usage: flint seed --demo [--reset] [--yes]",
	"usage.setup":                "usage: flint setup [--dry] [--json] [--update-rules] [--src PATH] [--exposure normal|minimal] [--mcp]",
	"usage.sync":                 "usage: flint sync remote add <name> <url> | flint sync remote list | flint sync remote remove <name> | flint sync push <remote> | flint sync pull <remote> [--dry] | flint sync reset --to-remote <name> [--yes]",
	"usage.teardown":             "usage: flint teardown [--purge] [--yes]",
	"usage.update":               "usage: flint update [--check] [--insecure-skip-signature]",
	"usage.write":                "usage: flint write <kind> [--source %s] [--session ID] — kind: knowledge|session|project, payload: JSON/JSONL on stdin",
	"err.unknown_command":        "unknown command: %s",
	"err.unknown_flag":           "unknown flag: %s",
	"err.out_requires_path":      "--out requires a path",
	"err.file_requires_path":     "--file requires a path",
	"err.vault_requires_path":    "--vault requires a path",
	"err.days_requires_number":   "report: --days requires a number",
	"err.limit_non_negative":     "--limit must be a non-negative integer: %q",
	"err.demo_required":          "seed: --demo required",
	"err.check_requires_name":    "doctor: --check requires a name",
	"err.encrypt_requires_out":   "--encrypt requires --out (ciphertext is file-only)",
	"err.restore_requires_file":  "restore requires --file",
	"tui.read.title":             "Read search",
	"tui.read.query":             "Search query",
	"tui.read.cancelled":         "read cancelled",
	"tui.read.empty_query":       "empty query — nothing to search",
	"tui.read.no_results":        "no results — try another query",
	"tui.read.no_selection":      "no item selected",
	"tui.read.score":             "score %s",
	"tui.read.why":               "why %s",
	"tui.read.hint.list":         "enter detail · esc back · q quit",
	"tui.read.hint.list.short":   "enter/esc · q quit",
	"tui.read.hint.detail":       "esc back · q quit",
	"tui.read.hint.detail.short": "esc · q quit",
}

var ko = map[string]string{
	"usage.line":                 `{"usage":"flint <%s> [인자]"}`,
	"usage.line.plain":           "flint <%s> [인자]",
	"usage.approve":              "사용법: flint approve --id N [--supersede OLD]",
	"usage.audit":                "사용법: flint audit [--limit N] [--session ID] [--stat] [--telemetry]",
	"usage.backup":               "사용법: flint backup [--out 파일] [--encrypt] [--mask]",
	"usage.brief":                "사용법: flint brief [쿼리] [--limit N]",
	"usage.compact":              "사용법: flint compact (훅 진입점 — 플래그 없음)",
	"usage.diff":                 "사용법: flint diff <from> <to> [--summary] [--data|--schema]",
	"usage.distill":              "사용법: flint distill --out 디렉터리 [--min-score N]",
	"usage.doctor":               "사용법: flint doctor [--list-checks] [--check 이름] [--fix] [--json]",
	"usage.done":                 "사용법: flint done --session <id>",
	"usage.export":               "사용법: flint export [--format jsonl|md|txt|csv|pam] [--out 파일] [--status active|all] [--tag X] [--since RFC3339] [--sign (pam 전용)]",
	"usage.hot":                  "사용법: flint hot [--session ID] [--full]",
	"usage.import":               "사용법: flint import [경로] [--commit] | flint import --format jsonl|pam [파일]",
	"usage.import.format":        "사용법: flint import --format jsonl|pam [파일]",
	"usage.index":                "사용법: flint index [--vault 경로] [--limit N]",
	"usage.maintain":             "사용법: flint maintain --check | --dup-suspects [--limit M] | --conflict-suspects [--limit M] | --stale [--days N] [--limit M] | --invalidate-stale [--yes] [--days N] [--limit M] | --invalidate (--id N --reason S | --ids 1,2,3 --yes) | --merge --from N --to M | --supersede OLD --by NEW | --expire-pending N [--commit] | --meta-audit | --prune-invalidated [N] [--commit] | --decay-audit [--limit M] | --age-audit [--limit M] | --merge-audit [--limit M] | --recall-audit [--limit M] | --reaffirm (--id N | --ids 1,2) --yes | --recheck N [--conditions S --yes] | --gc [--shallow] --yes | --purge-dropped [--yes] | --tui (옵트인, 단독 실행)",
	"usage.nudge":                "사용법: flint nudge [--clear]",
	"usage.pending":              "사용법: flint pending [--tui]",
	"usage.pending.json":         "사용법: flint pending [--json]",
	"usage.profile":              "사용법: flint profile [--apply normal|minimal]",
	"usage.read":                 "사용법: flint read [쿼리] [--json] [--fields F] [--limit N] [--status S] [--as-of T] [--session ID] [--source X] [--tag T] [--entity E] [--domain D] [--min-confidence N] [--origin agent|human] [--related N] [--why]",
	"usage.report":               "사용법: flint report [--tui] | [--days N] [--html] [--out 파일] [--text]",
	"usage.report.args":          "사용법: flint report [--days N] [--html] [--out 파일] [--text]",
	"usage.restore":              "사용법: flint restore --file 파일 [--drill]",
	"usage.seed":                 "사용법: flint seed --demo [--reset] [--yes]",
	"usage.setup":                "사용법: flint setup [--dry] [--json] [--update-rules] [--src 경로] [--exposure normal|minimal] [--mcp]",
	"usage.sync":                 "사용법: flint sync remote add <이름> <url> | flint sync remote list | flint sync remote remove <이름> | flint sync push <원격> | flint sync pull <원격> [--dry] | flint sync reset --to-remote <이름> [--yes]",
	"usage.teardown":             "사용법: flint teardown [--purge] [--yes]",
	"usage.update":               "사용법: flint update [--check] [--insecure-skip-signature]",
	"usage.write":                "사용법: flint write <종류> [--source %s] [--session ID] — 종류: knowledge|session|project, 페이로드: stdin의 JSON/JSONL",
	"err.unknown_command":        "알 수 없는 명령: %s",
	"err.unknown_flag":           "알 수 없는 플래그: %s",
	"err.out_requires_path":      "--out에는 경로가 필요합니다",
	"err.file_requires_path":     "--file에는 경로가 필요합니다",
	"err.vault_requires_path":    "--vault에는 경로가 필요합니다",
	"err.days_requires_number":   "report: --days에는 숫자가 필요합니다",
	"err.limit_non_negative":     "--limit은 0 이상 정수여야 합니다: %q",
	"err.demo_required":          "seed: --demo가 필요합니다",
	"err.check_requires_name":    "doctor: --check에는 이름이 필요합니다",
	"err.encrypt_requires_out":   "--encrypt에는 --out이 필요합니다 (암호문은 파일 전용)",
	"err.restore_requires_file":  "restore에는 --file이 필요합니다",
	"tui.read.title":             "읽기 검색",
	"tui.read.query":             "검색 쿼리",
	"tui.read.cancelled":         "읽기 취소됨",
	"tui.read.empty_query":       "빈 쿼리 — 검색할 내용이 없습니다",
	"tui.read.no_results":        "결과 없음 — 다른 쿼리로 시도하세요",
	"tui.read.no_selection":      "선택된 항목 없음",
	"tui.read.score":             "점수 %s",
	"tui.read.why":               "근거 %s",
	"tui.read.hint.list":         "enter 상세 · esc 뒤로 · q 종료",
	"tui.read.hint.list.short":   "enter/esc · q 종료",
	"tui.read.hint.detail":       "esc 뒤로 · q 종료",
	"tui.read.hint.detail.short": "esc · q 종료",
}

var Catalog = map[string]map[string]string{
	"en": en,
	"ko": ko,
}

func Lang() string {
	lang := strings.ToLower(strings.TrimSpace(os.Getenv("FLINT_LANG")))
	if _, ok := Catalog[lang]; ok {
		return lang
	}
	return "en"
}

func T(key string) string {
	if s, ok := Catalog[Lang()][key]; ok {
		return s
	}
	if s, ok := Catalog["en"][key]; ok {
		return s
	}
	return key
}
