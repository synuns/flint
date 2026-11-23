//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestParallel8WritersAllCommitted(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	bin := filepath.Join(root, "bin", "flint")
	build := exec.Command("go", "build", "-o", bin, "./cmd/flint")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build 실패: %v; out=%s", err, b)
	}

	warmup := exec.Command(bin, "write", "knowledge")
	warmup.Env = os.Environ()
	warmup.Stdin = strings.NewReader(
		`{"topic":"e2e-par-warmup","summary":"병렬 웜업","confidence":0.9}`)
	if werr := warmupRun(warmup); werr != nil {

		pool := mustDB(t)
		cleanupTopics(t, pool, "e2e-par-")
		t.Fatalf("웜업 write 실패: %v", werr)
	}

	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-par-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-par-") })

	const n = 8
	before := countPrefix(t, pool, "e2e-par-")

	type result struct {
		idx     int
		out     string
		code    int
		elapsed time.Duration
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	runStart := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := `{"topic":"e2e-par-k` + strconv.Itoa(i) +
				`","summary":"병렬 쓰기 프로브 ` + strconv.Itoa(i) + `","confidence":0.9}`
			start := time.Now()
			cmd := exec.Command(bin, "write", "knowledge")
			cmd.Env = os.Environ()
			cmd.Stdin = strings.NewReader(payload)
			var out strings.Builder
			cmd.Stdout = &out
			cmd.Stderr = &out
			code := 0
			if err := cmd.Run(); err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					code = ee.ExitCode()
				} else {
					code = -1
				}
			}
			results[i] = result{idx: i, out: out.String(), code: code, elapsed: time.Since(start)}
		}(i)
	}
	wg.Wait()
	wall := time.Since(runStart)

	exit1, committed, dup := 0, 0, 0
	hashes := make(map[string]bool, n)
	for _, r := range results {
		if r.code == 1 {
			exit1++
		}
		if r.code != 0 {
			t.Errorf("writer[%d] exit=%d, want 0; out=%s", r.idx, r.code, r.out)
			continue
		}
		m := decode(t, r.out)
		switch m["status"] {
		case "committed":
			committed++
			h, _ := m["hash"].(string)
			if h == "" {
				t.Errorf("writer[%d] hash 미노출; out=%s", r.idx, r.out)
				continue
			}
			if hashes[h] {
				t.Errorf("writer[%d] content_hash 중복(%s)", r.idx, h)
			}
			hashes[h] = true
		case "duplicate":
			dup++
		default:
			t.Errorf("writer[%d] status=%v; out=%s", r.idx, m["status"], r.out)
		}
	}
	if exit1 != 0 {
		t.Errorf("exit 1 발생 %d건, want 0", exit1)
	}
	if committed != n {
		t.Errorf("committed=%d, want %d(duplicate=%d)", committed, n, dup)
	}

	got := prefixHashes(t, pool, "e2e-par-")
	if len(got) != before+n {
		t.Fatalf("knowledge 행 +%d, want +%d(손실/중복)", len(got)-before, n)
	}
	for h := range hashes {
		if !got[h] {
			t.Errorf("출력 hash %s가 DB에 없음(손실)", h)
		}
	}
	for h := range got {
		if !hashes[h] {
			t.Errorf("DB hash %s가 출력에 없음(중복/외부 행)", h)
		}
	}

	conflicts := 0
	var maxElapsed time.Duration
	for _, r := range results {
		if r.elapsed >= 900*time.Millisecond {
			conflicts++
		}
		if r.elapsed > maxElapsed {
			maxElapsed = r.elapsed
		}
	}
	rate := float64(conflicts) / float64(n)
	t.Logf("N=%d wall=%v committed=%d duplicate=%d exit1=%d conflicts=%d(%.1f%%) max_elapsed=%v",
		n, wall.Round(time.Millisecond), committed, dup, exit1, conflicts, rate*100, maxElapsed.Round(time.Millisecond))
}

func warmupRun(cmd *exec.Cmd) error {
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v; out=%s", err, b)
	}
	return nil
}

func countPrefix(t *testing.T, pool *pgxpool.Pool, prefix string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM knowledge WHERE topic LIKE $1`, prefix+"%").
		Scan(&n); err != nil {
		t.Fatalf("count 실패(%s): %v", prefix, err)
	}
	return n
}

func prefixHashes(t *testing.T, pool *pgxpool.Pool, prefix string) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT content_hash FROM knowledge WHERE topic LIKE $1`, prefix+"%")
	if err != nil {
		t.Fatalf("hash 조회 실패(%s): %v", prefix, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("hash 스캔 실패: %v", err)
		}
		if out[h] {
			t.Fatalf("DB content_hash 중복: %s", h)
		}
		out[h] = true
	}
	return out
}
