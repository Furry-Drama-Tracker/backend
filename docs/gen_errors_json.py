#!/usr/bin/env python3
"""扫描后端源码中所有错误码调用点，生成 docs/errors.json 注册表。

用法（在 backend/ 目录下）:
    python3 docs/gen_errors_json.py

覆盖模式：errors/apperrors 的 NewC/NewKeyC/NewExtraC/WrapC/Abort500C/AbortErrC/
AbortCoded/AbortKeyed，middleware 的 codedBody，以及渲染器兜底 SYS0001 与
panic 恢复 SYS0500。新增错误码后重跑本脚本即可刷新注册表（输出字节稳定，
可安全提交）。"""
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCAN_DIRS = ["internal/handler", "internal/service", "internal/middleware", "internal/server", "internal/errors"]
CTORS = {"NewC", "NewKeyC", "NewExtraC", "WrapC", "Abort500C", "AbortErrC", "AbortCoded", "AbortKeyed"}
CODE_RE = re.compile(r'"([A-Z]{3}\d{4})"')
HTTP_CONST = {
    "http.StatusBadRequest": 400, "http.StatusUnauthorized": 401,
    "http.StatusForbidden": 403, "http.StatusNotFound": 404,
    "http.StatusMethodNotAllowed": 405, "http.StatusConflict": 409,
    "http.StatusRequestEntityTooLarge": 413, "http.StatusUnsupportedMediaType": 415,
    "http.StatusUnprocessableEntity": 422, "http.StatusTooManyRequests": 429,
    "http.StatusInternalServerError": 500, "http.StatusGatewayTimeout": 504,
}


def find_close(s, i):
    """s[i] 为 '(' 或 '{'，返回配对闭合下标；i 越界返回 -1。"""
    depth, in_str = 0, None
    while i < len(s):
        ch = s[i]
        if in_str:
            if in_str == '"' and ch == "\\":
                i += 2
                continue
            if ch == in_str:
                in_str = None
        elif ch in '"`\'':
            in_str = ch
        elif ch in "([{":
            depth += 1
        elif ch in ")]}":
            depth -= 1
            if depth == 0:
                return i
        i += 1
    return -1


def split_args(s):
    args, depth, cur, in_str = [], 0, [], None
    i = 0
    while i < len(s):
        ch = s[i]
        if in_str:
            cur.append(ch)
            if in_str == '"' and ch == "\\" and i + 1 < len(s):
                cur.append(s[i + 1])
                i += 2
                continue
            if ch == in_str:
                in_str = None
        elif ch in '"`\'':
            in_str = ch
            cur.append(ch)
        elif ch in "([{":
            depth += 1
            cur.append(ch)
        elif ch in ")]}":
            depth -= 1
            cur.append(ch)
        elif ch == "," and depth == 0:
            args.append("".join(cur).strip())
            cur = []
        else:
            cur.append(ch)
        i += 1
    tail = "".join(cur).strip()
    if tail:
        args.append(tail)
    return args


def line_of(text, idx):
    return text.count("\n", 0, idx) + 1


# 边缘/外部系统预留的错误码（非本仓库生成，源码扫描扫不到，手工登记）。
# ref 恒为 0 = 边缘响应标记（无 RequestRef，无服务端请求日志可查）。
# 完整响应信封（含 esa_request_id/esa_rule）见 docs/esa-error.schema.json，
# ESA 错误 ID 注册表见 docs/esa-errors.csv。
RESERVED = [
    {
        "code": "ESA0001", "kind": "edge", "status": 451,
        "message": "此服务在您所在地区不可用。",
        "file": "（ESA 边缘配置，非本仓库）", "line": None,
        "_note": "ESA 地区限制拦截；ref 恒为 0",
    },
]


def read_source(path):
    """读取待扫描源码：未提交修改（WIP，含并行会话）的文件按 HEAD 内容扫描——
    注册表只反映已提交契约，WIP 的临时重复码不得阻塞或混入注册表。"""
    rel = path.relative_to(ROOT).as_posix()
    st = subprocess.run(["git", "status", "--porcelain", "--", rel],
                        capture_output=True, text=True, cwd=ROOT)
    if st.returncode == 0 and st.stdout.strip():
        show = subprocess.run(["git", "show", f"HEAD:{rel}"],
                              capture_output=True, text=True, cwd=ROOT)
        if show.returncode == 0:
            print(f"  [HEAD] {rel}（工作树有未提交修改，按已提交内容扫描）", file=sys.stderr)
            return show.stdout
    return path.read_text()


def scan_file(path):
    """返回该文件的 entries。跳过注释行与函数定义行；WIP 文件按 HEAD 内容扫描。"""
    text = read_source(path)

    def in_comment_or_def(idx):
        ls = text.rfind("\n", 0, idx) + 1
        stripped = text[ls:idx + 60].split("\n")[0].lstrip()
        return stripped.startswith("//") or stripped.startswith("func ")

    entries = []
    i = 0
    while True:
        m = re.search(r"\b(?:errors|apperrors)\.(NewC|NewKeyC|NewExtraC|WrapC|Abort500C|AbortErrC|AbortCoded|AbortKeyed)\(", text[i:])
        if not m:
            break
        kind = m.group(1)
        open_idx = i + m.end() - 1
        close = find_close(text, open_idx)
        if close == -1:
            break
        if in_comment_or_def(i + m.start()):
            i = close
            continue
        args = split_args(text[open_idx + 1:close])
        ln = line_of(text, i + m.start())
        status, msg, msg_key = None, "<dynamic>", None
        if kind == "Abort500C":
            # (c, code, msg, cause, isDev)
            code = args[1].strip('"') if len(args) > 1 else ""
            status = 500
            msg = args[2] if len(args) > 2 else "<dynamic>"
        elif kind in ("AbortErrC", "AbortCoded", "AbortKeyed"):
            # (c, status, code, msg[, cause|key], isDev)
            code = args[2].strip('"') if len(args) > 2 else ""
            try:
                status = int(args[1])
            except (ValueError, IndexError):
                status = None
            msg = args[3] if len(args) > 3 else "<dynamic>"
            if kind == "AbortKeyed" and len(args) > 4:
                msg_key = args[4].strip('"')
        else:
            # NewC/NewKeyC/NewExtraC/WrapC: (code, status, msg[, ...])
            code = args[0].strip('"') if args else ""
            try:
                status = int(args[1])
            except (ValueError, IndexError):
                status = None
            msg = args[2] if len(args) > 2 else "<dynamic>"
            if kind == "NewKeyC" and len(args) > 3:
                msg_key = args[3].strip('"')
        entries.append({
            "code": code,
            "kind": kind,
            "status": status,
            "message": json.loads(msg) if (msg.startswith('"') and msg.endswith('"')) else "<dynamic>",
            **({"messageKey": msg_key} if msg_key else {}),
            "file": path.relative_to(ROOT).as_posix(),
            "line": ln,
        })
        i = close
    # codedBody（middleware）
    i = 0
    while True:
        m = re.search(r"\bcodedBody\(", text[i:])
        if not m:
            break
        open_idx = i + m.end() - 1
        close = find_close(text, open_idx)
        if close == -1:
            break
        if in_comment_or_def(i + m.start()):
            i = close
            continue
        args = split_args(text[open_idx + 1:close])
        ln = line_of(text, i + m.start())
        # codedBody(c, status, code, gin.H{...})；status 为数字或 http.StatusXxx
        cm = CODE_RE.search(args[2]) if len(args) > 2 else None
        try:
            st_raw = args[1]
            st = int(st_raw) if st_raw.isdigit() else HTTP_CONST.get(st_raw)
        except (ValueError, IndexError):
            st = None
        msg = "<dynamic>"
        mb = re.search(r'"message":\s*("(?:[^"\\]|\\.)*")', text[open_idx + 1:close])
        if mb:
            msg = json.loads(mb.group(1))
        entries.append({
            "code": cm.group(1) if cm else "",
            "kind": "codedBody",
            "status": st,
            "message": msg,
            "file": path.relative_to(ROOT).as_posix(),
            "line": ln,
        })
        i = close
    return entries


def main():
    entries = []
    for d in SCAN_DIRS:
        for p in sorted((ROOT / d).glob("*.go")):
            if p.name.endswith("_test.go"):
                continue
            entries.extend(scan_file(p))
    entries.extend(RESERVED)
    # 渲染器兜底与 panic 恢复（不在构造器清单里）
    ae_text = (ROOT / "internal/errors/apperror.go").read_text()
    ae_line = next(i + 1 for i, l in enumerate(ae_text.split("\n")) if 'fallbackCode = "SYS0001"' in l)
    entries.append({
        "code": "SYS0001", "kind": "fallback", "status": None,
        "message": "<dynamic>",
        "file": "internal/errors/apperror.go",
        "line": ae_line,
        "_note": "未编码 4xx/5xx 的兜底错误码（渲染器 fallbackCode）",
    })
    app_text = (ROOT / "internal/server/app.go").read_text()
    app_line = next(i + 1 for i, l in enumerate(app_text.split("\n")) if '"error": "SYS0500"' in l)
    entries.append({
        "code": "SYS0500", "kind": "panic", "status": 500,
        "message": "<dynamic>",
        "file": "internal/server/app.go",
        "line": app_line,
        "_note": "panic 恢复（CustomRecovery）",
    })
    seen, dedup = set(), []
    for e in entries:
        key = (e["code"], e["file"], e["line"])
        if key in seen:
            continue
        seen.add(key)
        dedup.append(e)
    codes = [e["code"] for e in dedup]
    dupes = {c for c in codes if codes.count(c) > 1}
    if dupes:
        print("重复错误码:", sorted(dupes), file=sys.stderr)
        sys.exit(1)
    out = {
        "$schema": "./errors.schema.json",
        "version": 1,
        "refSpec": {"type": "integer", "range": [0, 999999999],
                    "display": "出错编号 = error 码 + '-' + 补零 9 位整数，如 USR0007-000123456；展示与复制由前端拼装（frontend/src/utils/errorMeta.js）"},
        "codes": sorted(dedup, key=lambda e: e["code"]),
    }
    target = Path(__file__).resolve().parent / "errors.json"
    target.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n")
    print(f"generated {target} ({len(dedup)} codes)")


if __name__ == "__main__":
    main()
