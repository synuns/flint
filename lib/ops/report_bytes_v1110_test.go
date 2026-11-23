package ops

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

func bytesDailyFake(todayJSON string) *fakeStore {
	today := "daily:" + time.Now().UTC().Format("2006-01-02")
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "LIKE 'daily:%'"):
			return [][]any{{today, todayJSON}}, nil
		case strings.Contains(sql, "FROM read_events"):
			return nil, nil
		case strings.Contains(sql, "FROM meta WHERE key = 'schema_version'"):
			return [][]any{{strconv.Itoa(db.LatestSchemaVersion)}}, nil
		case strings.Contains(sql, "FROM meta"):
			return nil, nil
		case strings.Contains(sql, "hot_events"),
			strings.Contains(sql, "session_starts"),
			strings.Contains(sql, "ingested_at >="),
			strings.Contains(sql, "id, topic FROM knowledge"):
			return nil, nil
		}
		return [][]any{{int64(0), int64(0), int64(0)}}, nil
	}}
}

func TestReportBytes(t *testing.T) {
	fixture := `{"read":4,"hot":2,"brief":3,"read_bytes":1000,"hot_bytes":900,"brief_bytes":600}`
	var out strings.Builder
	if code := RunReport(context.Background(), bytesDailyFake(fixture),
		[]string{"--days", "1"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	var rep struct {
		Effect struct {
			Bytes struct {
				Read struct {
					Bytes14d int `json:"bytes_14d"`
					AvgBytes int `json:"avg_bytes"`
				} `json:"read"`
				Hot struct {
					Bytes14d int `json:"bytes_14d"`
					AvgBytes int `json:"avg_bytes"`
				} `json:"hot"`
				Brief struct {
					Bytes14d int `json:"bytes_14d"`
					AvgBytes int `json:"avg_bytes"`
				} `json:"brief"`
			} `json:"bytes"`
		} `json:"effect"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rep); err != nil {
		t.Fatalf("report JSON 파싱: %v (%s)", err, out.String())
	}
	if got := rep.Effect.Bytes.Read; got.Bytes14d != 1000 || got.AvgBytes != 250 {
		t.Errorf("bytes.read=%+v, want {1000 250}", got)
	}
	if got := rep.Effect.Bytes.Hot; got.Bytes14d != 900 || got.AvgBytes != 450 {
		t.Errorf("bytes.hot=%+v, want {900 450}", got)
	}
	if got := rep.Effect.Bytes.Brief; got.Bytes14d != 600 || got.AvgBytes != 200 {
		t.Errorf("bytes.brief=%+v, want {600 200}", got)
	}
}

func TestReportBytesZeroDenominator(t *testing.T) {
	fixture := `{"read":0,"hot":0,"brief":0,"read_bytes":0,"hot_bytes":500,"brief_bytes":0}`
	var out strings.Builder
	if code := RunReport(context.Background(), bytesDailyFake(fixture),
		[]string{"--days", "1"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	var rep struct {
		Effect struct {
			Bytes struct {
				Hot struct {
					Bytes14d int `json:"bytes_14d"`
					AvgBytes int `json:"avg_bytes"`
				} `json:"hot"`
			} `json:"bytes"`
		} `json:"effect"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rep); err != nil {
		t.Fatalf("report JSON 파싱: %v (%s)", err, out.String())
	}
	if got := rep.Effect.Bytes.Hot; got.Bytes14d != 500 || got.AvgBytes != 0 {
		t.Errorf("bytes.hot=%+v, want {500 0}(분모 0 → avg 0)", got)
	}
}

func TestStatusBytes14d(t *testing.T) {
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return "", nil })
	fixture := `{"read":4,"hot":2,"read_bytes":1000,"hot_bytes":900}`
	var out strings.Builder
	if code := RunStatus(context.Background(), bytesDailyFake(fixture), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("status exit=%d: %s", code, out.String())
	}
	var got struct {
		HotBytes14d  int `json:"hot_bytes_14d"`
		ReadBytes14d int `json:"read_bytes_14d"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got); err != nil {
		t.Fatalf("status JSON 파싱: %v (%s)", err, out.String())
	}
	if got.HotBytes14d != 900 {
		t.Errorf("hot_bytes_14d=%d, want 900", got.HotBytes14d)
	}
	if got.ReadBytes14d != 1000 {
		t.Errorf("read_bytes_14d=%d, want 1000", got.ReadBytes14d)
	}
}
