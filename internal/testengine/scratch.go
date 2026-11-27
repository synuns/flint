package testengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
)

var createDBFn func(ctx context.Context, name string) error = defaultCreateDB

func defaultCreateDB(ctx context.Context, name string) error {
	conn, err := connectDSN(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "CREATE DATABASE "+name)
	return err
}

func scratchName() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "flint_scratch_" + hex.EncodeToString(buf), nil
}

func Scratch(t *testing.T) string {
	ctx := context.Background()
	if err := EnsureEngine(ctx); err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 기동 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("전용 테스트 엔진 기동 불가 — 실DB 테스트 skip: %v", err)
	}
	name, err := scratchName()
	if err != nil {
		t.Fatalf("스크래치 DB명 생성 실패: %v", err)
	}
	if err := createDBFn(ctx, name); err != nil {
		t.Fatalf("CREATE DATABASE %s: %v", name, err)
	}
	RegisterScratch(name)
	scratchOnTestCleanup(t, name)
	u, err := url.Parse(ResolveDSN())
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func CloneTarget(t *testing.T) (name, dsn string) {
	if err := EnsureEngine(context.Background()); err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 기동 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("전용 테스트 엔진 기동 불가 — 실DB 테스트 skip: %v", err)
	}
	name, err := scratchName()
	if err != nil {
		t.Fatalf("스크래치 DB명 생성 실패: %v", err)
	}
	RegisterScratch(name)
	scratchOnTestCleanup(t, name)
	u, err := url.Parse(ResolveDSN())
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	return name, u.String()
}

func scratchOnTestCleanup(t *testing.T, name string) {
	t.Cleanup(func() {
		t.Cleanup(func() {
			if err := dropScratch(context.Background(), name); err != nil {
				fmt.Fprintf(os.Stderr, "경고: 스크래치 %s 개별 DROP 실패(%v) — RunCleanup 재시도\n", name, err)
				return
			}
			scratchMu.Lock()
			for i, n := range scratchDBs {
				if n == name {
					scratchDBs = append(scratchDBs[:i], scratchDBs[i+1:]...)
					break
				}
			}
			scratchMu.Unlock()
		})
	})
}
