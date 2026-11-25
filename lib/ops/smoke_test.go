package ops

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/wire"
)

func withSmokeSeams(t *testing.T, hook DoctorCheck) {
	t.Helper()
	prev := smokeHookDetectFn
	smokeHookDetectFn = func() DoctorCheck { return hook }
	t.Cleanup(func() { smokeHookDetectFn = prev })
}

func smokeFakeHook() DoctorCheck {
	return DoctorCheck{Name: "hook-file", Status: "ok", Detail: "fake hook registered"}
}

func smokeEnvScratch(t *testing.T) string {
	t.Helper()
	scratch := testengine.Scratch(t)
	t.Setenv("FLINT_DSN", scratch)
	t.Setenv("FLINT_DEMO_DSN", "")
	os.Unsetenv("FLINT_DEMO_DSN")
	return scratch
}

func smokeStepByName(steps []SmokeStep, name string) SmokeStep {
	for _, s := range steps {
		if s.Step == name {
			return s
		}
	}
	return SmokeStep{Step: name, Status: "missing"}
}

func TestSmokeSequence(t *testing.T) {
	ctx := context.Background()
	smokeEnvScratch(t)
	withSmokeSeams(t, smokeFakeHook())

	steps, code := smokeSequence(ctx)
	if code != 0 {
		t.Fatalf("smoke exit=%d — steps: %+v", code, steps)
	}

	wantSteps := []string{"connect", "hook", "demo-db", "write", "read", "inject", "cleanup"}
	if len(steps) != len(wantSteps) {
		t.Fatalf("steps = %d (%+v), want %d — 미보고 단계 존재", len(steps), steps, len(wantSteps))
	}
	seen := map[string]bool{}
	for i, name := range wantSteps {
		if steps[i].Step != name || steps[i].Status != "ok" {
			t.Fatalf("step[%d] = %+v, want %s ok", i, steps[i], name)
		}
		seen[steps[i].Step] = true
	}
	if len(seen) != len(wantSteps) {
		t.Fatal("중복 단계 행 존재")
	}

	demoRepo, err := openSmokeStore(ctx, wire.ResolveDSN())
	if err != nil {
		t.Fatalf("demo store reopen: %v", err)
	}
	if _, err := demoRepo.InsertBatch(ctx, []map[string]any{
		{"kind": "knowledge", "topic": "demo/user-seeded", "summary": "seed --demo 행 — smoke 청소 대상 아님", "tags": ":demo:", "confidence": 0.9, "content_hash": "smoke-user-seeded"},
	}); err != nil {
		t.Fatalf("foreign demo row seed: %v", err)
	}
	demoRepo.Close()

	steps, code = smokeSequence(ctx)
	if code != 0 {
		t.Fatalf("smoke 재실행 exit=%d — 시드된 demo 행 공존 시 smoke 실패: %+v", code, steps)
	}
	demoRepo, err = openSmokeStore(ctx, wire.ResolveDSN())
	if err != nil {
		t.Fatalf("demo store reopen 2: %v", err)
	}
	defer demoRepo.Close()
	n := smokeCountRows(ctx, demoRepo, "demo")
	if n != 1 {
		t.Errorf("demo DB foreign 행 = %d, want 1 — smoke가 demo 소유 외 행을 청소", n)
	}

	var buf strings.Builder
	code = RunSmokeCheck(ctx, &buf)
	if code != 0 {
		t.Fatalf("RunSmokeCheck exit=%d — out: %s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{`"step":"write"`, `"step":"read"`, `"step":"inject"`, `"step":"cleanup"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("구조화 출력에 %s 없음:\n%s", want, out)
		}
	}
}

func TestSmokeProdIsolation(t *testing.T) {
	ctx := context.Background()
	prod := smokeEnvScratch(t)

	prodDB := dsnPathName(prod)
	demoDSN, err := smokeResolveDemoDSNFn()
	if err != nil {
		t.Fatalf("demo DSN derivation: %v", err)
	}
	if got := dsnPathName(demoDSN); got != prodDB+"_demo" {
		t.Fatalf("demo db = %q, want %q (격리 파생 고정)", got, prodDB+"_demo")
	}
	if demoDSN == prod {
		t.Fatal("demo DSN이 운영 DSN과 동일 — 격리 붕괴")
	}

	withSmokeSeams(t, smokeFakeHook())
	steps, code := smokeSequence(ctx)
	if code != 0 {
		t.Fatalf("smoke exit=%d — %+v", code, steps)
	}

	prodRepo, err := openSmokeStore(ctx, prod)
	if err != nil {
		t.Fatalf("prod store: %v", err)
	}
	defer prodRepo.Close()
	n := smokeCountRows(ctx, prodRepo, prodDB)
	if n != 0 {
		t.Fatalf("운영 DB knowledge 행 = %d, want 0 — smoke가 운영 DB에 기록", n)
	}

	t.Run("refuse-demo-equals-prod", func(t *testing.T) {
		t.Setenv("FLINT_DEMO_DSN", prod)
		steps, code := smokeSequence(ctx)
		if code == 0 {
			t.Fatal("demo DSN == prod DSN 인데 통과 — 거부 계약 위반")
		}
		if s := smokeStepByName(steps, "demo-db"); s.Status != "fail" {
			t.Fatalf("demo-db step = %+v, want fail(refusal)", s)
		}
		if n := smokeCountRows(ctx, prodRepo, prodDB); n != 0 {
			t.Fatalf("거부 후에도 운영 DB 오염: %d행", n)
		}
	})
}

func TestSmokeDoctorCheckReportsFailedStep(t *testing.T) {
	ctx := context.Background()
	smokeEnvScratch(t)
	withSmokeSeams(t, smokeFakeHook())
	prevDial := smokeDialFn
	smokeDialFn = func(context.Context) bool { return false }
	t.Cleanup(func() { smokeDialFn = prevDial })

	c := runSmokeDoctorCheck(ctx)
	if c.Status != "fail" {
		t.Fatalf("doctor smoke status = %q, want fail", c.Status)
	}
	if !strings.Contains(c.Detail, "connect") {
		t.Errorf("doctor smoke detail = %q, want 실패 단계(connect) 보존", c.Detail)
	}
	if c.Fix == "" {
		t.Error("doctor smoke fix 안내 미제공")
	}
}

func TestSmokeRegistered(t *testing.T) {
	specs := SmokeChecks()
	if len(specs) != 1 {
		t.Fatalf("SmokeChecks = %d건, want 1", len(specs))
	}
	s := specs[0]
	if s.Name != "smoke" {
		t.Fatalf("name = %q, want smoke", s.Name)
	}
	if s.Fn == nil {
		t.Fatal("smoke check Fn 미결합 — 등록표 계약 위반")
	}
	for _, ds := range doctorChecks() {
		if ds.Name == s.Name {
			t.Fatal("기본 doctorChecks()에 smoke 존재 — 온보딩 서브모드 상시 실행 위반(G 룰링)")
		}
	}
	if _, ok := doctorSpec("smoke"); ok {
		t.Fatal("doctorSpec(smoke) 등록됨 — 기본 등록표 온보딩 배제 계약 위반")
	}
	if !smokeAddressable("smoke") {
		t.Fatal("smoke --check 주소화 실패 — 옵트인 경로 상실")
	}

	ctx := context.Background()
	smokeEnvScratch(t)
	withSmokeSeams(t, smokeFakeHook())
	c := s.Fn(ctx, nil)
	if c.Name != "smoke" || (c.Status != "ok" && c.Status != "fail") {
		t.Fatalf("doctor surface = %+v, want name=smoke status ok|fail", c)
	}
}

func TestSmokeHookFailStops(t *testing.T) {
	ctx := context.Background()
	smokeEnvScratch(t)
	withSmokeSeams(t, DoctorCheck{Name: "hook-file", Status: "fail", Detail: "settings.json not found"})
	prevDial := smokeDialFn
	smokeDialFn = func(context.Context) bool { return true }
	t.Cleanup(func() { smokeDialFn = prevDial })

	steps, code := smokeSequence(ctx)
	if code == 0 {
		t.Fatalf("hook 실패인데 smoke exit=0 — 실패가 종료 코드에 미반영: %+v", steps)
	}
	s := smokeStepByName(steps, "hook")
	if s.Status != "fail" || s.Detail == "" {
		t.Fatalf("hook step = %+v, want fail + detail 보존", s)
	}
	if len(steps) != 2 || steps[0].Step != "connect" {
		t.Fatalf("steps = %+v, want [connect hook(fail)] — hook 실패에서 시퀀스 정지", steps)
	}
	if got := smokeStepByName(steps, "demo-db"); got.Status != "missing" {
		t.Errorf("hook 실패 후 demo-db 이후 단계 실행됨: %+v", got)
	}
}

func TestSmokeSameEngineReject(t *testing.T) {
	ctx := context.Background()
	prod := smokeEnvScratch(t)

	u, err := url.Parse(prod)
	if err != nil {
		t.Fatalf("prod DSN 파싱: %v", err)
	}
	u.Path = "/flint_shadow"
	t.Setenv("FLINT_DEMO_DSN", u.String())

	withSmokeSeams(t, smokeFakeHook())
	prevDial := smokeDialFn
	smokeDialFn = func(context.Context) bool { return true }
	t.Cleanup(func() { smokeDialFn = prevDial })
	prevEnsure := smokeEnsureDBFn
	smokeEnsureDBFn = func(context.Context, string, string) error {
		t.Error("운영 엔진에 demo-db 접촉(ensure) — 동일 엔진 거부가 접속 전에 발동해야 함")
		return errors.New("guard must reject before ensure")
	}
	t.Cleanup(func() { smokeEnsureDBFn = prevEnsure })
	prevStore := smokeNewStoreFn
	smokeNewStoreFn = func(context.Context, string) (wire.Store, error) {
		t.Error("동일 엔진 비demo DB에 store 개설 — 거부 계약 위반(migrate·기록 발생)")
		return nil, errors.New("guard must reject before store")
	}
	t.Cleanup(func() { smokeNewStoreFn = prevStore })

	steps, code := smokeSequence(ctx)
	if code == 0 {
		t.Fatalf("운영 엔진 비demo DB demo DSN이 통과 — 거부 계약 위반: %+v", steps)
	}
	s := smokeStepByName(steps, "demo-db")
	if s.Status != "fail" {
		t.Fatalf("demo-db step = %+v, want fail(동일 엔진 거부)", s)
	}
	if !strings.Contains(s.Detail, smokeDemoDSNSuffix) {
		t.Errorf("거부 상세에 %s 조건 안내 없음: %s", smokeDemoDSNSuffix, s.Detail)
	}
}

func TestSmokeQuickstartDocsync(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "QUICKSTART.md"))
	if err != nil {
		t.Fatalf("QUICKSTART.md: %v", err)
	}
	doc := string(b)
	if !strings.Contains(doc, "doctor --check smoke") {
		t.Error("QUICKSTART.md에 smoke 절차 행(doctor --check smoke) 없음")
	}
	if !strings.Contains(doc, "plugin") || !strings.Contains(doc, "flint update") {
		t.Error("QUICKSTART.md에 플러그인 갱신 안내 행(plugin-version-gap → flint update) 없음")
	}
}
