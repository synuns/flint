package ops

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func checkUpdate(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "update"}
	if !ValidVersionFn(CurrentVersionFn()) {
		c.Status = "ok"
		c.Detail = "build version not finalized (dev) — verdict deferred"
		return c
	}
	latest, err := FetchLatestVersionFn()
	cur := CurrentVersionFn()
	if err != nil {
		c.Status = "ok"
		c.Detail = "cannot check the latest version (possibly offline) — verdict deferred"
		return c
	}
	if CompareVersionsFn(latest, cur) > 0 {
		c.Status = "warn"
		c.Detail = "v" + cur + " → v" + latest + " outdated"
		c.Fix = "fix: flint update"
		return c
	}
	c.Status = "ok"
	c.Detail = "v" + cur + " up to date"
	return c
}

func checkVersionsTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "versions"}
	cur := CurrentVersionFn()
	self := cur
	if !ValidVersionFn(cur) {
		self = cur + " (dev — not finalized)"
	}
	var parts []string
	parts = append(parts, "self v"+self)

	enginePart, engineWarn, engineFix := engineVersionPart(ctx, st)
	parts = append(parts, enginePart)

	cachePart := "cache latest unknown"
	ageH := -1
	if UpdateCacheInfoFn != nil {
		if latest, checkedAt := UpdateCacheInfoFn(); latest != "" {
			cachePart = "cache latest v" + latest
			if t, terr := time.Parse(time.RFC3339, checkedAt); terr == nil {
				ageH = int(time.Since(t).Hours())
				cachePart += fmt.Sprintf(" (%dh old)", ageH)
			}
		}
	}
	parts = append(parts, cachePart)

	pathPart := "PATH flint not found"
	if p, lerr := exec.LookPath("flint"); lerr == nil {
		pathPart = "PATH " + p
	}
	parts = append(parts, pathPart)

	regPart := "registry unverified (offline)"
	if FetchRegistryVersionFn != nil {
		if v, rerr := FetchRegistryVersionFn(); rerr == nil && v != "" {
			regPart = "registry v" + v
		} else if RegistryCacheInfoFn != nil {
			if cv, checkedAt := RegistryCacheInfoFn(); cv != "" {
				regPart = "registry v" + cv + " (cached, unverified — offline"
				if t, terr := time.Parse(time.RFC3339, checkedAt); terr == nil {
					regPart += fmt.Sprintf(", %dh old", int(time.Since(t).Hours()))
				}
				regPart += ")"
			}
		}
	}
	parts = append(parts, regPart)
	c.Detail = strings.Join(parts, " · ")

	if engineWarn != "" {
		c.Status = "warn"
		c.Detail += " — " + engineWarn
		c.Fix = engineFix
		return c
	}
	if warnH := th.UpdateCacheAgeH; ageH >= 0 && warnH > 0 && ageH > warnH {
		c.Status = "warn"
		c.Detail += fmt.Sprintf(" — cache age %dh exceeds update_cache_age_h %dh", ageH, warnH)
		c.Fix = "fix: flint doctor --check update (refresh the version cache)"
		return c
	}
	c.Status = "ok"
	return c
}
