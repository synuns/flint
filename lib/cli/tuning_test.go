// tuning_test.go — v0.23.0(D③): tuning 커맨드 계약 — list/get/set/reset·
// 실경로 값 검증(폴백 값 거부)·알려지지 않은 키 exit 3.
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// tuningFake — meta IN 조회·upsert·delete를 기록하는 fake.
type tuningFake struct {
	*fakeStore
	meta map[string]string
	sets []string
	dels []string
}

func newTuningFake() *tuningFake {
	return &tuningFake{fakeStore: &fakeStore{}, meta: map[string]string{}}
}

func (f *tuningFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	switch {
	case strings.Contains(sql, "key IN ("):
		rows := [][]any{}
		for _, k := range f.meta {
			_ = k
		}
		for k, v := range f.meta {
			if containsKey(args, k) {
				rows = append(rows, [][]any{{k, v}}[0])
			}
		}
		return rows, nil
	case strings.Contains(sql, "INSERT INTO meta"):
		f.sets = append(f.sets, fmt.Sprint(args[0], "=", args[1]))
		f.meta[fmt.Sprint(args[0])] = fmt.Sprint(args[1])
		return nil, nil
	case strings.Contains(sql, "DELETE FROM meta"):
		f.dels = append(f.dels, fmt.Sprint(args[0]))
		delete(f.meta, fmt.Sprint(args[0]))
		return nil, nil
	}
	// loadThresholds의 meta IN 조회는 위 key IN 케이스가 담당 — 여기
	// 도달하면 fixture 라우팅 계약 위반(Fatalf 불가 — 에러로 식별).
	return nil, fmt.Errorf("tuningFake 미매칭: %s", sql)
}

func containsKey(args []any, k string) bool {
	for _, a := range args {
		if fmt.Sprint(a) == k {
			return true
		}
	}
	return false
}

func TestTuningSetValidatesThroughRealParse(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	// 유효 값 — 저장.
	if code := runTuning(context.Background(), f, []string{"set", "dead_days", "45"}, nil, &out); code != 0 {
		t.Fatalf("set exit=%d: %s", code, out.String())
	}
	if len(f.sets) != 1 || f.meta["dead_days"] != "45" {
		t.Fatalf("sets=%v meta=%v", f.sets, f.meta)
	}
	// 무효 값 — 파싱 폴백 감지·거부(exit 3, 미기록).
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "dead_days", "many"}, nil, &out); code != 3 {
		t.Fatalf("무효 값 exit=%d, want 3: %s", code, out.String())
	}
	if len(f.sets) != 1 {
		t.Fatalf("무효 값까지 기록: %v", f.sets)
	}
}

func TestTuningUnknownKeyAndList(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"get", "nonexistent_key"}, nil, &out); code != 3 {
		t.Fatalf("unknown key exit=%d, want 3", code)
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"list"}, nil, &out); code != 0 {
		t.Fatalf("list exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"key":"dead_days"`) || !strings.Contains(out.String(), `"override":false`) {
		t.Errorf("list 출력 계약 위반: %s", out.String())
	}
	// reset — 오버라이드 set 후 삭제.
	runTuning(context.Background(), f, []string{"set", "stale_days", "60"}, nil, &out)
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"reset", "stale_days"}, nil, &out); code != 0 {
		t.Fatalf("reset exit=%d: %s", code, out.String())
	}
	if len(f.dels) != 1 || f.dels[0] != "stale_days" {
		t.Fatalf("dels=%v", f.dels)
	}
}

// TestTuningKeysMatchRegistry — v0.23.0 리뷰 ⑧: tuningKeys와
// thresholdsKeys(공유 소스)의 동질성 — 새 키 추가 시 3중 유지 드리프트 방어.
func TestTuningKeysMatchRegistry(t *testing.T) {
	reg := map[string]bool{}
	for _, k := range thresholdsKeys() {
		reg[k] = true
	}
	km := tuningKeyMap()
	if len(km) != len(reg) {
		t.Fatalf("키 수 불일치: tuning %d vs registry %d", len(km), len(reg))
	}
	for k := range km {
		if !reg[k] {
			t.Errorf("tuning 전용 키 %q — thresholdsKeys에 없음", k)
		}
	}
}

// TestTuningSetRejectsZero — 리뷰 ②: 폴백 키의 0은 "미설정" 의미라
// 표현 불가 — 거부되어야 한다(v0.5 4키 제외).
func TestTuningSetRejectsZero(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"set", "dead_days", "0"}, nil, &out); code != 3 {
		t.Fatalf("dead_days=0 exit=%d, want 3: %s", code, out.String())
	}
	if len(f.sets) != 0 {
		t.Fatalf("0이 기록됨: %v", f.sets)
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "pending_max", "0"}, nil, &out); code != 0 {
		t.Fatalf("pending_max=0(실값 유효) exit=%d: %s", code, out.String())
	}
}

// TestTuningMetaKeysSetReset — v0.24.0 R1d/R4a: thresholds 밖 meta 키의
// set 검증(열거·정수·목록)과 reset --all.
func TestTuningMetaKeysSetReset(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	// exposure_profile 열거 검증.
	if code := runTuning(context.Background(), f, []string{"set", "exposure_profile", "loud"}, nil, &out); code != 3 {
		t.Fatalf("무효 열거 exit=%d: %s", code, out.String())
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "exposure_profile", "minimal"}, nil, &out); code != 0 {
		t.Fatalf("minimal set exit=%d: %s", code, out.String())
	}
	// vocab 목록 검증 — 빈 원소 거부·정상 목록 허용.
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "vocab_tags", "a,,b"}, nil, &out); code != 3 {
		t.Fatalf("빈 원소 exit=%d: %s", code, out.String())
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "vocab_tags", "ops,k8s"}, nil, &out); code != 0 {
		t.Fatalf("정상 목록 exit=%d: %s", code, out.String())
	}
	// reset --all — 전체 삭제.
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"reset", "--all"}, nil, &out); code != 0 {
		t.Fatalf("reset --all exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"reset-all"`) {
		t.Fatalf("reset --all 출력: %s", out.String())
	}
}

// TestTuningSetEquivalentFloatNotation — v0.25.3(미커버 스윕 ③):
// '0.30'처럼 수치는 유효하되 포맷이 다른 표기가 문자열 비교로 거부되던
// 것을 수치 비교로 수용.
func TestTuningSetEquivalentFloatNotation(t *testing.T) {
	f := newTuningFake()
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"set", "conf_floor", "0.30"}, nil, &out); code != 0 {
		t.Fatalf("0.30 exit=%d(수치 유효): %s", code, out.String())
	}
	if f.meta["conf_floor"] != "0.30" {
		t.Fatalf("기록값 = %v", f.meta)
	}
	out.Reset()
	if code := runTuning(context.Background(), f, []string{"set", "conf_floor", "1.5"}, nil, &out); code != 3 {
		t.Fatalf("범위 밖 1.5 exit=%d, want 3: %s", code, out.String())
	}
}
