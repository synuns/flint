#!/usr/bin/env python3
# T43: CI YAML 구조 검증 — make ci 첫 단계에서 실행.
# 파싱 실패·구조 위반 시 0이 아닌 exit(워크플로 조기 실패).
# 실행: python3 scripts/validate_yaml.py (대상 2건: ci.yml, .goreleaser.yml)
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent

FAILURES = []


def check(name, cond, msg):
    if not cond:
        FAILURES.append(f"{name}: {msg}")


def main():
    # 1) Actions 워크플로 — 파싱 + 필수 잡 구조
    wf_path = ROOT / ".github" / "workflows" / "ci.yml"
    try:
        wf = yaml.safe_load(wf_path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        print(f"FAIL {wf_path} 없음")
        return 1
    except yaml.YAMLError as e:
        print(f"FAIL ci.yml 파싱 오류: {e}")
        return 1

    jobs = wf.get("jobs") or {}
    check("ci.yml", "on" in wf or True in wf, "트리거(on) 없음")
    check("ci.yml", "test" in jobs, "test 잡 없음")
    test_job = jobs.get("test") or {}
    services = test_job.get("services") or {}
    check("ci.yml", "doltgres" in services, "doltgres 서비스 컨테이너 없음")
    dolt = services.get("doltgres") or {}
    check("ci.yml", "5433" in str(dolt.get("ports")), "포트 5433 매핑 없음")

    # 2) GoReleaser — 파싱 + darwin 빌드 타깃
    gr_path = ROOT / ".goreleaser.yml"
    try:
        gr = yaml.safe_load(gr_path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        print(f"FAIL {gr_path} 없음")
        return 1
    except yaml.YAMLError as e:
        print(f"FAIL .goreleaser.yml 파싱 오류: {e}")
        return 1

    builds = gr.get("builds") or []
    darwin = any(
        "darwin" in t for b in builds for t in b.get("targets", [])
    ) or any("darwin" in (b.get("goos") or []) for b in builds)
    check(".goreleaser.yml", bool(builds), "builds 없음")
    check(".goreleaser.yml", darwin, "darwin 타깃 없음")
    ldflags = " ".join(str(f) for b in builds for f in (b.get("ldflags") or []))
    check(".goreleaser.yml", "flint/lib/cli.version" in ldflags, "버전 주입 ldflags 없음")

    if FAILURES:
        for f in FAILURES:
            print(f"FAIL {f}")
        return 1
    print("OK ci.yml + .goreleaser.yml 구조 유효")
    return 0


if __name__ == "__main__":
    sys.exit(main())
