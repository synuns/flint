#!/usr/bin/env python3
import datetime
import json
import os
import re
import sys

SDK_MARKERS = (
    "openai",
    "anthropic",
    "langchain",
    "cohere",
    "llama",
    "mistral",
    "ollama",
    "genai",
    "dashscope",
    "zhipuai",
    "chatglm",
    "huggingface",
    "tiktoken",
    "litellm",
    "transformers",
)

WHITELIST = {}
WHITELIST_ENV = "FLINT_LLM_IMPORT_WHITELIST"
SKIP_DIRS = {".git", "node_modules", "bin"}

IMPORT_BLOCK_LINE = re.compile(r'^\s*(?:_\s+|\.\s+|[A-Za-z_]\w*\s+)?"([^"]+)"')
IMPORT_SINGLE = re.compile(r'^\s*import\s+(?:_\s+|\.\s+|[A-Za-z_]\w*\s+)?"([^"]+)"')
WAIVER_KEYWORD = "flint-waiver:"
WAIVER_EXPIRES = re.compile(r"expires=(\d{4}-\d{2}-\d{2})")


def load_whitelist():
    raw = os.environ.get(WHITELIST_ENV)
    if not raw:
        return WHITELIST
    with open(raw, encoding="utf-8") as f:
        return json.load(f)


def check_disclaimer(path, entry):
    if not isinstance(entry, dict):
        return f"[llm-import] 화이트리스트 불량: {path} — 면책 행(이유·만료일) 필요"
    reason = entry.get("reason")
    if not isinstance(reason, str) or not reason.strip():
        return f"[llm-import] 화이트리스트 불량: {path} — 면책 이유 필수"
    expires = entry.get("expires")
    if not isinstance(expires, str):
        return f"[llm-import] 화이트리스트 불량: {path} — 면책 만료일 필수(YYYY-MM-DD)"
    try:
        day = datetime.date.fromisoformat(expires)
    except ValueError:
        return f"[llm-import] 화이트리스트 불량: {path} — 면책 만료일 형식 YYYY-MM-DD(현재 {expires})"
    if day < datetime.date.today():
        return f"[llm-import] 화이트리스트 불량: {path} — 면책 만료일 경과({expires}) — 갱신 또는 제거"
    return None


def parse_waiver_comment(s):
    idx = s.find(WAIVER_KEYWORD)
    if idx < 0:
        return None
    body = s[idx + len(WAIVER_KEYWORD):]
    m = WAIVER_EXPIRES.search(body)
    expires = m.group(1) if m else ""
    reason = body[: m.start()] if m else body
    reason = reason.strip().strip("—-").strip()
    return reason, expires


def waiver_defect(w):
    reason, expires = w
    if not reason:
        return "면책 이유 누락 — flint-waiver: <이유> expires=YYYY-MM-DD(docs/CI_REVIEW.md §6)"
    if not expires:
        return "면책 만료일 필수(expires=YYYY-MM-DD)"
    try:
        day = datetime.date.fromisoformat(expires)
    except ValueError:
        return f"면책 만료일 형식 YYYY-MM-DD(현재 {expires})"
    if day < datetime.date.today():
        return f"면책 만료일 경과({expires}) — 갱신 또는 제거"
    return None


def go_files(root):
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for name in filenames:
            if name.endswith(".go"):
                yield os.path.join(dirpath, name)


def scan_file(path):
    hits = []
    with open(path, encoding="utf-8") as f:
        in_block = False
        in_comment = False
        waiver_on_prev = None
        for n, line in enumerate(f, 1):
            s = line.strip()
            if in_comment:
                end = s.find("*/")
                if end < 0:
                    waiver_on_prev = None
                    continue
                in_comment = False
                s = s[end + 2:].strip()
                if not s:
                    waiver_on_prev = None
                    continue
            if s.startswith("//"):
                w = parse_waiver_comment(s)
                waiver_on_prev = w
                continue
            if in_block:
                if s.startswith("/*"):
                    rest = s[2:]
                    end = rest.find("*/")
                    if end < 0:
                        in_comment = True
                    else:
                        rest = rest[end + 2:].strip()
                        if rest.startswith(")"):
                            in_block = False
                        elif rest:
                            m = IMPORT_BLOCK_LINE.match(rest)
                            if m:
                                hits.append((n, m.group(1), waiver_on_prev))
                    waiver_on_prev = None
                    continue
                if s.startswith(")"):
                    in_block = False
                    waiver_on_prev = None
                    continue
                m = IMPORT_BLOCK_LINE.match(line)
                if m:
                    hits.append((n, m.group(1), waiver_on_prev))
                waiver_on_prev = None
                continue
            if s.startswith("/*") or s.startswith("*"):
                waiver_on_prev = None
                continue
            if s.startswith("import ("):
                in_block = True
                waiver_on_prev = None
                continue
            m = IMPORT_SINGLE.match(line)
            if m:
                hits.append((n, m.group(1), waiver_on_prev))
            waiver_on_prev = None
    return hits


def is_sdk(path):
    low = path.lower()
    return any(marker in low for marker in SDK_MARKERS)


def report_violation(root, path, line, imp, defect):
    rel = os.path.relpath(path, root)
    why = "이유: 외부 LLM SDK import — 제품 코드 LLM 유입 금지(T2, 결정론 검사)"
    if defect:
        why += " — 면책 행 불량: " + defect
    print(f"[llm-import] 위반: {rel}:{line}")
    print(f"import: {imp}")
    print(why)
    print("수정처: SDK 의존 제거 — 외부 프로세스·플러그인 훅 경계로 격리하거나, 기한부 기술 부채면 위반 import 바로 위 줄에 flint-waiver: <이유> expires=YYYY-MM-DD 면책 행(docs/CI_REVIEW.md §6)")
    print(f"요약: {rel}:{line} {imp} → 수정처 참조, exit 3")


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    try:
        whitelist = load_whitelist()
    except (OSError, ValueError) as e:
        print(f"[llm-import] 화이트리스트 로드 실패: {e}")
        sys.exit(3)
    for path, entry in sorted(whitelist.items()):
        bad = check_disclaimer(path, entry)
        if bad:
            print(bad)
            sys.exit(3)
    failed = False
    for path in go_files(root):
        for line, imp, waiver in scan_file(path):
            if not is_sdk(imp) or imp in whitelist:
                continue
            defect = waiver_defect(waiver) if waiver else ""
            if waiver and defect is None:
                continue
            report_violation(root, path, line, imp, defect)
            failed = True
    sys.exit(3 if failed else 0)


if __name__ == "__main__":
    main()
