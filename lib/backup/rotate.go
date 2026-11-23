package backup

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const BackupKeepKey = "backup_keep"

const DefaultBackupKeep = 14

func BackupKeep(getKV func(string) (string, error)) int {
	if getKV == nil {
		return DefaultBackupKeep
	}
	v, err := getKV(BackupKeepKey)
	if err != nil || strings.TrimSpace(v) == "" {
		return DefaultBackupKeep
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil || n < 1 {
		log.Printf("[flint] backup: %s=%q invalid — default %d", BackupKeepKey, v, DefaultBackupKeep)
		return DefaultBackupKeep
	}
	return n
}

var flintDumpName = regexp.MustCompile(`^flint-\d{8}-\d{6}-\d+\.(sql|sql\.age|age)$`)

func Rotate(dir string, keep int) ([]string, error) {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("backup rotate: %w", err)
	}

	type dump struct {
		name string
		mod  time.Time
	}
	var dumps []dump
	for _, e := range entries {

		if e.IsDir() || !isDumpExt(filepath.Ext(e.Name())) || !flintDumpName.MatchString(e.Name()) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			return nil, fmt.Errorf("backup rotate: %s: %w", e.Name(), ierr)
		}
		dumps = append(dumps, dump{name: e.Name(), mod: info.ModTime()})
	}
	sort.Slice(dumps, func(i, j int) bool { return dumps[i].mod.After(dumps[j].mod) })
	if len(dumps) <= keep {
		return nil, nil
	}

	var removed []string
	for _, d := range dumps[keep:] {
		if rerr := os.Remove(filepath.Join(dir, d.name)); rerr != nil {
			return removed, fmt.Errorf("backup rotate: %w", rerr)
		}
		removed = append(removed, d.name)
		side := d.name + ".sha256"
		if _, serr := os.Stat(filepath.Join(dir, side)); serr == nil {
			if rerr := os.Remove(filepath.Join(dir, side)); rerr != nil {
				return removed, fmt.Errorf("backup rotate: %w", rerr)
			}
			removed = append(removed, side)
		}
	}
	return removed, nil
}

func isDumpExt(ext string) bool {
	return ext == ".sql" || ext == ".age"
}
