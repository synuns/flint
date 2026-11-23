package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/internal/testengine"
)

const (
	echoE2ETopic     = "데모 프로젝트 운영 기록"
	echoE2EDupSum    = "회의록 오늘 확정된 마일스톤은 3개다"
	echoE2EContraSum = "회의록 다음 주는 전체 휴가 기간이다"
	echoE2EOtherSum  = "텔레비전 편성표 조정 작업 기록과 성수기 특별 편성 반영시 검토해야 할 광고 단가 구간표 초안 내용을 함께 기록한다"
)

func TestWriteEchoE2ERealSlow(t *testing.T) {
	_, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	freshDSN := testengine.Scratch(t)

	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return newRealStore(ctx, freshDSN)
	}
	t.Cleanup(func() { newStore = prev })

	run := func(topic, summary string, conf float64) map[string]any {
		t.Helper()
		var out strings.Builder
		line := fmt.Sprintf(`{"topic":%q,"summary":%q,"confidence":%f}`+"\n", topic, summary, conf)
		code := Run([]string{"write", "knowledge"}, strings.NewReader(line), &out)
		if code != 0 {
			t.Fatalf("write exit=%d out=%s", code, out.String())
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
			t.Fatalf("출력 파싱: %v — %s", err, out.String())
		}
		return m
	}

	m := run(echoE2ETopic, echoE2EDupSum, 0.9)
	if m["status"] != "committed" || m["state"] != "active" {
		t.Fatalf("적립 #1=%v, want committed/active", m)
	}
	seedID, _ := m["id"].(float64)
	if seedID < 1 {
		t.Fatalf("적립 #1 id=%v, want 1 이상", m["id"])
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("빈 저장소 첫 쓰기에 echo 부재해야 함: %v", m)
	}

	m = run(echoE2ETopic, echoE2EDupSum, 0.9)
	if m["status"] != "duplicate" {
		t.Fatalf("재전송=%v, want duplicate", m)
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("정확-hash 중복 응답에 echo 부재해야 함: %v", m)
	}

	m = run(echoE2ETopic, echoE2EContraSum, 0.9)
	if m["status"] != "committed" {
		t.Fatalf("상이 주장 적립=%v, want committed", m)
	}
	echo, ok := m["echo"].([]any)
	if !ok || len(echo) < 1 {
		t.Fatalf("echo=%v, want 1건 이상", m["echo"])
	}
	c, _ := echo[0].(map[string]any)
	if c["id"] != seedID {
		t.Errorf("echo 후보 id=%v, want 시드 %v", c["id"], seedID)
	}
	if c["relation"] != "contradicts" {
		t.Errorf("echo relation=%v, want contradicts", c["relation"])
	}
	if _, ok := c["score"]; !ok {
		t.Errorf("echo 후보 score 필드 부재: %v", c)
	}
	if _, ok := c["similarity"]; ok {
		t.Errorf("echo 후보에 similarity 필드는 부재해야 함: %v", c)
	}

	m = run(echoE2ETopic, echoE2EOtherSum, 0.9)
	if m["status"] != "committed" {
		t.Fatalf("이질 요약 적립=%v, want committed", m)
	}
	if _, ok := m["echo"]; ok {
		t.Errorf("임계 미달 쓰기에 echo 부재해야 함: %v", m)
	}

	m = run(echoE2ETopic, echoE2EContraSum+" (개정안)", 0.4)
	if m["status"] != "committed" || m["state"] != "pending" {
		t.Fatalf("저신뢰 적립=%v, want committed/pending", m)
	}
	pendID, _ := m["id"].(float64)
	var out strings.Builder
	code := Run([]string{"approve", "--id", fmt.Sprint(int(pendID)), "--supersede", fmt.Sprint(int(seedID))},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("approve --supersede exit=%d out=%s", code, out.String())
	}

	var ro strings.Builder
	if code := Run([]string{"read", echoE2ETopic, "--json"}, strings.NewReader(""), &ro); code != 0 {
		t.Fatalf("read exit=%d out=%s", code, ro.String())
	}
	if strings.Contains(ro.String(), fmt.Sprintf(`"id":%q`, fmt.Sprint(int(seedID)))) {
		t.Errorf("슈퍼시드된 시드 행이 여전히 활성: %s", ro.String())
	}
	if !strings.Contains(ro.String(), fmt.Sprintf(`"id":%q`, fmt.Sprint(int(pendID)))) {
		t.Errorf("승격 행이 활성 뷰에 부재: %s", ro.String())
	}
}
