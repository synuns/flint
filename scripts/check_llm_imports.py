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
        for n, line in enumerate(f, 1):
            s = line.strip()
            if s.startswith("//") or s.startswith("/*") or s.startswith("*"):
                continue
            if in_block:
                if s.startswith(")"):
                    in_block = False
                else:
                    m = IMPORT_BLOCK_LINE.match(line)
                    if m:
                        hits.append((n, m.group(1)))
                continue
            if s.startswith("import ("):
                in_block = True
                continue
            m = IMPORT_SINGLE.match(line)
            if m:
                hits.append((n, m.group(1)))
    return hits


def is_sdk(path):
    low = path.lower()
    return any(marker in low for marker in SDK_MARKERS)


def report_violation(root, path, line, imp):
    rel = os.path.relpath(path, root)
    print(f"[llm-import] 위반: {rel}:{line}")
    print(f"import: {imp}")
    print("이유: 외부 LLM SDK import — 제품 코드 LLM 유입 금지(T2, 결정론 검사)")
    print("수정처: SDK 의존 제거 — 외부 프로세스·플러그인 훅 경계로 격리하거나 등록표 화이트리스트 편입(scripts/check_llm_imports.py WHITELIST + 계약 테스트 동반)")
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
        for line, imp in scan_file(path):
            if not is_sdk(imp) or imp in whitelist:
                continue
            report_violation(root, path, line, imp)
            failed = True
    sys.exit(3 if failed else 0)


if __name__ == "__main__":
    main()
