package db

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHandshakeDoltgres(t *testing.T) {
	f := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "version()") {
			return [][]any{{"PostgreSQL 15.5"}}, nil
		}
		return [][]any{{int64(1)}}, nil
	}}
	info, err := Handshake(context.Background(), f)
	if err != nil {
		t.Fatalf("Handshake err: %v", err)
	}
	if info.Kind != "doltgres" {
		t.Fatalf("Kind=%q, want doltgres", info.Kind)
	}
	if info.Version != "15.5" {
		t.Fatalf("Version=%q, want 15.5(파싱된 버전)", info.Version)
	}
}

func TestHandshakeVanillaPostgres(t *testing.T) {
	f := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "version()") {
			return [][]any{{"PostgreSQL 16.2 (Ubuntu 16.2-1.pgdg22.04+1)"}}, nil
		}
		return nil, errors.New(`relation "dolt_log" does not exist`)
	}}
	info, err := Handshake(context.Background(), f)
	if err != nil {
		t.Fatalf("Handshake err: %v", err)
	}
	if info.Kind != "postgres" {
		t.Fatalf("Kind=%q, want postgres(dolt_log 부재)", info.Kind)
	}
	if info.Version != "16.2" {
		t.Fatalf("Version=%q, want 16.2", info.Version)
	}
}

func TestHandshakeNoResponse(t *testing.T) {
	f := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, errors.New("connection refused")
	}}
	_, err := Handshake(context.Background(), f)
	if err == nil {
		t.Fatal("무응답인데 오류 없음")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("원인 미포함: %v", err)
	}
}

func TestHandshakeEmptyVersion(t *testing.T) {
	f := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	if _, err := Handshake(context.Background(), f); err == nil {
		t.Fatal("빈 version()인데 오류 없음")
	}
}

func TestHandshakeNilStore(t *testing.T) {
	if _, err := Handshake(context.Background(), nil); err == nil {
		t.Fatal("nil store인데 오류 없음")
	}
}

func TestHandshakeDoltgresProbeQueries(t *testing.T) {
	var queries []string
	f := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		queries = append(queries, sql)
		if strings.Contains(sql, "version()") {
			return [][]any{{"unknown-engine 2.0"}}, nil
		}
		return nil, errors.New("no table")
	}}
	info, err := Handshake(context.Background(), f)
	if err != nil {
		t.Fatalf("Handshake err: %v", err)
	}
	if info.Kind != "" {
		t.Fatalf("Kind=%q, want 빈값(프로브 실패·비PG 문자열)", info.Kind)
	}
	if info.Version != "2.0" {
		t.Fatalf("Version=%q, want 2.0", info.Version)
	}
	if len(queries) < 2 {
		t.Fatalf("쿼리 %d회, want ≥2(version()+프로브): %v", len(queries), queries)
	}
	if !strings.Contains(queries[0], "version()") {
		t.Fatalf("첫 쿼리=%q, want version()", queries[0])
	}
	if !strings.Contains(queries[1], DoltLogTable) {
		t.Fatalf("프로브 쿼리=%q, want %s 참조", queries[1], DoltLogTable)
	}
}
