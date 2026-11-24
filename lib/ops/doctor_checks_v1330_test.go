package ops

import (
	"context"
	"testing"
)

func TestDoctorChecksRegistered(t *testing.T) {
	want := []string{
		"hook-path", "dsn-class", "collection-freshness",
		"plugin-version-gap", "service-remnant",
	}
	for _, name := range want {
		spec, ok := doctorSpec(name)
		if !ok {
			t.Fatalf("doctor check %q 미등록 — 동질 가드 위반", name)
		}
		if spec.Chain != "tail" {
			t.Errorf("%s chain = %q, want tail", name, spec.Chain)
		}
	}
	tail := doctorTailChecks()
	if len(tail) < 5 {
		t.Fatalf("tail checks = %d종, 최소 5종 부재", len(tail))
	}
}

func TestDoctorFreshnessNilStoreWarn(t *testing.T) {
	spec, ok := doctorSpec("collection-freshness")
	if !ok {
		t.Fatal("collection-freshness 미등록")
	}
	if spec.OnStoreNil != "warn" {
		t.Fatalf("OnStoreNil = %q, want warn", spec.OnStoreNil)
	}
	c := runCheckSpec(context.Background(), nil, doctorThresholds(context.Background(), nil), spec)
	if c.Status != "warn" {
		t.Fatalf("st==nil status = %q, want warn", c.Status)
	}
}
