package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"

	dbref "github.com/synuns/flint/lib/db"
)

type dbState struct {
	logDepth int
	asOf     string
}

var containerDataDirPath = func() string {
	if d := os.Getenv("FLINT_ENGINE_CONTAINER_DATA"); d != "" {
		return d
	}
	return "/var/lib/docker/volumes/flint_doltgres-data/_data"
}

var copyDataDir = copyDataDirReal

var queryDBState = queryDBStateReal

func queryDBStateReal(ctx context.Context, mode string) (dbState, error) {
	dsn := os.Getenv("FLINT_DSN")
	if dsn == "" {
		return dbState{}, ErrNotWired
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return dbState{}, err
	}
	defer pool.Close()
	var depth int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+dbref.DoltLogTable).Scan(&depth); err != nil {
		return dbState{}, err
	}
	var asOf string
	if err := pool.QueryRow(ctx, "SELECT COALESCE(max(commit_hash), '') FROM "+dbref.DoltLogTable).Scan(&asOf); err != nil {
		return dbState{}, err
	}
	return dbState{logDepth: depth, asOf: asOf}, nil
}

var downloadEngine = Download

func ContainerToNative(ctx context.Context) error {
	if containerService.Stop == nil || containerService.Start == nil || nativeService.Start == nil {
		return ErrNotWired
	}
	if queryDBState == nil || copyDataDir == nil {
		return ErrNotWired
	}
	home := engineHomeDir()
	if home == "" {
		return errors.New("engine: home directory unavailable — set FLINT_HOME")
	}
	before, err := queryDBState(ctx, ModeManagedContainer)
	if err != nil {
		return fmt.Errorf("engine: baseline read from container: %w", err)
	}
	if err := containerService.Stop(); err != nil {
		return err
	}
	rollback := func(err error) error {
		if nativeService.Stop != nil {
			if serr := nativeService.Stop(); serr != nil {
				err = errors.Join(err, fmt.Errorf("engine: native stop during rollback: %w", serr))
			}
		}
		if rerr := containerService.Start(); rerr != nil {
			return errors.Join(err, fmt.Errorf("engine: container restart during rollback: %w", rerr))
		}
		return err
	}
	if err := copyDataDir(containerDataDirPath(), filepath.Join(home, "engine", "data")); err != nil {
		return rollback(fmt.Errorf("engine: copy container data: %w", err))
	}
	if err := nativeService.Start(); err != nil {
		return rollback(fmt.Errorf("engine: start native: %w", err))
	}
	after, err := queryDBState(ctx, ModeNative)
	if err != nil {
		return rollback(fmt.Errorf("engine: verify read from native: %w", err))
	}
	if after != before {
		mismatch := fmt.Errorf(
			fmt.Sprintf("engine: native data not equivalent — %s depth %%d→%%d, as of %%q→%%q", dbref.DoltLogTable),
			before.logDepth, after.logDepth, before.asOf, after.asOf,
		)
		if serr := SetMode(ModeManagedContainer); serr != nil {
			return rollback(errors.Join(mismatch, fmt.Errorf("engine: restore mode: %w", serr)))
		}
		return rollback(mismatch)
	}
	return SetMode(ModeNative)
}

func copyDataDirReal(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("engine: container data source: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("engine: container data source is not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dst, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return copyFileMode(path, target, fi.Mode().Perm())
	})
}

func copyFileMode(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func UpgradeRoundTrip(ctx context.Context, fromVer, toVer string) error {
	if fromVer == "" || toVer == "" {
		return errors.New("engine: upgrade requires both from and to versions")
	}
	if downloadEngine == nil {
		return ErrNotWired
	}
	home := engineHomeDir()
	if home == "" {
		return errors.New("engine: home directory unavailable — set FLINT_HOME")
	}
	engDir := filepath.Join(home, "engine")
	if _, err := os.Stat(filepath.Join(engDir, fromVer)); err != nil {
		return fmt.Errorf("engine: from-version %s not installed: %w", fromVer, err)
	}
	rollback := func(err error) error {
		if rerr := setCurrentVersion(engDir, fromVer); rerr != nil {
			return errors.Join(err, fmt.Errorf("engine: symlink rollback to %s: %w", fromVer, rerr))
		}
		if nativeService.Stop != nil {
			_ = nativeService.Stop()
		}
		if rerr := nativeService.Start(); rerr != nil {
			return errors.Join(err, fmt.Errorf("engine: service restart during rollback: %w", rerr))
		}
		return err
	}
	if _, err := downloadEngine(ctx, toVer); err != nil {
		return rollback(fmt.Errorf("engine: deploy %s: %w", toVer, err))
	}
	if nativeService.Start == nil {
		return rollback(ErrNotWired)
	}
	if err := nativeService.Start(); err != nil {
		return rollback(fmt.Errorf("engine: start native %s: %w", toVer, err))
	}
	return setCurrentVersion(engDir, fromVer)
}

func setCurrentVersion(engDir, ver string) error {
	tmp := filepath.Join(engDir, ".current-tmp")
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(ver, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(engDir, "current"))
}
