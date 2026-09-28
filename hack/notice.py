#!/usr/bin/env python3
"""Keep NOTICE in step with the MPL-2.0 modules compiled into the provider.

usage:
  hack/notice.py list  <binary>...           print the NOTICE module lines
  hack/notice.py check <NOTICE> <binary>...  fail when NOTICE misses a module or lists a stale one

A module counts as MPL-2.0 when a LICENSE/LICENCE/COPYING file at its root in
the Go module cache is the Mozilla Public License 2.0. The module list comes
from `go version -m`, so it is exactly what the binary embeds.
"""

import json
import os
import re
import subprocess
import sys

LICENSE_FILE = re.compile(r"(?i)^(licen[cs]e|copying)(\.[a-z]+)?$")
PSEUDO = re.compile(r"-(?:0\.)?\d{14}-([0-9a-f]{12})(?:\+incompatible)?$")
MAJOR = re.compile(r"^v\d+$")
NOTICE_LINE = re.compile(r"^\s+(\S+)\s+(v\S+)\s+https://\S+\s*$")


def go(*args):
    return subprocess.check_output(["go", *args], text=True)


def modules(binaries):
    """(path, version) of every module a binary embeds; replacements win."""
    mods = set()
    for b in binaries:
        dep = None
        for line in go("version", "-m", b).splitlines():
            f = line.split()
            if len(f) >= 3 and f[0] == "dep":
                dep = (f[1], f[2])
                mods.add(dep)
            elif len(f) >= 3 and f[0] == "=>" and dep is not None:
                mods.discard(dep)
                mods.add((f[1], f[2]))
    return mods


def escape(p):
    return re.sub(r"[A-Z]", lambda m: "!" + m.group(0).lower(), p)


def module_dir(cache, path, version):
    d = os.path.join(cache, escape(path) + "@" + escape(version))
    if os.path.isdir(d):
        return d
    info = json.loads(go("mod", "download", "-json", f"{path}@{version}"))
    return info["Dir"]


def is_mpl2(d):
    for name in os.listdir(d):
        p = os.path.join(d, name)
        if LICENSE_FILE.match(name) and os.path.isfile(p):
            with open(p, encoding="utf-8", errors="replace") as f:
                head = " ".join(f.read(4096).split())
            if "Mozilla Public License" in head and "2.0" in head:
                return True
    return False


def source_url(path, version):
    """The upstream source at the exact tag or commit the binary embeds."""
    parts = path.split("/")
    if parts[0] != "github.com" or len(parts) < 3:
        return f"https://proxy.golang.org/{escape(path)}/@v/{escape(version)}.zip"
    repo = "/".join(parts[:3])
    sub = parts[3:]
    if sub and MAJOR.match(sub[-1]):
        sub = sub[:-1]
    subdir = "/".join(sub)
    m = PSEUDO.search(version)
    if m:
        ref = m.group(1)
    elif subdir:
        ref = f"{subdir}/{version}"  # nested modules are tagged <dir>/<version>
    else:
        ref = version
    return f"https://{repo}/tree/{ref}" + (f"/{subdir}" if subdir else "")


def mpl_modules(binaries):
    cache = go("env", "GOMODCACHE").strip()
    return sorted((p, v) for p, v in modules(binaries) if is_mpl2(module_dir(cache, p, v)))


def main(argv):
    if len(argv) >= 2 and argv[0] == "list":
        mods = mpl_modules(argv[1:])
        width = max(len(f"{p} {v}") for p, v in mods)
        for p, v in mods:
            print(f"  {p + ' ' + v:<{width}}  {source_url(p, v)}")
        return 0
    if len(argv) >= 3 and argv[0] == "check":
        with open(argv[1], encoding="utf-8") as f:
            listed = {tuple(m.groups()) for m in map(NOTICE_LINE.match, f) if m}
        want = set(mpl_modules(argv[2:]))
        missing = sorted(want - listed)
        stale = sorted(listed - want)
        for p, v in missing:
            print(f"NOTICE is missing MPL-2.0 module {p} {v}", file=sys.stderr)
        for p, v in stale:
            print(f"NOTICE lists {p} {v}, which the binary does not embed", file=sys.stderr)
        if missing or stale:
            print("run: python3 hack/notice.py list <binary> and update NOTICE", file=sys.stderr)
            return 1
        print(f"NOTICE lists all {len(want)} MPL-2.0 modules")
        return 0
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
