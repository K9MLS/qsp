#!/usr/bin/env python3
"""Rewrite history so it holds no personal data, deriving what to replace.

**The working tree was scrubbed on 2026-09-16 and the history was not.** Other
operators' first names, towns and home addresses are in earlier commits, and
three P25 captures carried a household's mDNS, Syncthing and SSDP traffic until
0419. This rewrites every commit so none of it survives.

**It derives the replacement list; it does not store one.** A file listing club
members' names and home addresses would be the same disclosure in a different
place, and it would be committed here. So:

  * names and towns come from the scrub commit's own before-and-after pairs,
    word by word, keeping only strings that are absent from HEAD -- a word
    still in the tree was never sensitive, which is what separates a name from
    "the" in a reworded sentence;
  * addresses come from scanning every object for anything public and mapping
    each into a documentation range, which is the rule cmd/qsp/addresses_test.go
    enforces on the tree;
  * leaky captures are filtered by scripts/filter-capture.py, the same code
    that cleaned the working tree, applied to each historical blob on its own
    so history still shows what changed when.

Run it in a clone, look at what it proposes, then apply:

    scripts/scrub-history.py                 # derive and report, change nothing
    scripts/scrub-history.py --show          # also print the mapping
    scripts/scrub-history.py --apply         # rewrite, then verify

**--apply rewrites every commit and changes every hash.** Tag and bundle a
backup first; a force-push after this is not undoable.
"""

import argparse
import difflib
import importlib.util
import json
import os
import re
import subprocess
import sys
import tempfile

SCRUB_SUBJECT = "Other operators' names, towns and home addresses are out of the working tree"

# Dotted numbers that are not addresses. ETSI clause numbers are the same shape
# as a host, and cmd/qsp/addresses_test.go keeps the same list for the tree.
NOT_ADDRESSES = {"5.1.2.2", "5.1.2.3", "7.1.1.4", "7.1.1.5", "8.2.2.2"}

DOTTED = re.compile(r"\b(?:\d{1,3}\.){3}\d{1,3}\b")
# The same, for scanning objects, which come back as bytes -- captures included,
# since an address inside one is ASCII on the wire.
DOTTED_BYTES = re.compile(rb"\b(?:\d{1,3}\.){3}\d{1,3}\b")
IDENTITY = re.compile(
    rb"\x05local\x00|[A-Z0-9]{7}-[A-Z0-9]{7}-[A-Z0-9]{7}|uuid:[0-9a-f]{8}-|relay://"
)
# The ports a capture in testdata exists to document, as 0419 used.
RADIO_PORTS = ["41000", "41009", "42010", "42011", "42012", "42020", "6074", "32010"]


def git(*args, binary=False, repo="."):
    out = subprocess.run(["git", "-C", repo, *args], capture_output=True, check=True)
    return out.stdout if binary else out.stdout.decode("utf-8", "replace")


def is_public(s):
    """Whether an address is one the internet routes to somebody."""
    parts = s.split(".")
    if len(parts) != 4:
        return False
    try:
        octets = [int(p) for p in parts]
    except ValueError:
        return False
    if any(o > 255 for o in octets):
        return False
    if any(len(p) > 1 and p[0] == "0" for p in parts):
        return False  # a version string, not an address
    a, b, c, _ = octets
    if a in (0, 10, 127):
        return False
    if a == 172 and 16 <= b <= 31:
        return False
    if a == 192 and b == 168:
        return False
    if a == 100 and 64 <= b <= 127:
        return False  # carrier-grade NAT, which the docs discuss
    if a == 169 and b == 254:
        return False
    if a >= 224:
        return False
    if (a, b, c) in ((203, 0, 113), (198, 51, 100), (192, 0, 2)):
        return False  # documentation ranges, RFC 5737
    return True


def scrub_pairs(repo, subject):
    """The scrub commit's changed lines, as (before, after) pairs."""
    sha = git("log", "--all", "--format=%H", "--grep", subject, repo=repo).split()
    if not sha:
        return []
    diff = git("show", sha[0], "--", ".", ":!*.pcap", repo=repo).splitlines()
    pairs, removed, added = [], [], []

    def flush():
        pairs.extend(zip(removed, added))
        removed.clear()
        added.clear()

    for line in diff:
        if line.startswith("@@") or line.startswith("diff "):
            flush()
        elif line.startswith("-") and not line.startswith("---"):
            removed.append(line[1:])
        elif line.startswith("+") and not line.startswith("+++"):
            added.append(line[1:])
    flush()
    return pairs


def derive_words(repo, subject):
    """Names and towns, from what the scrub replaced them with."""
    mapping = {}
    for before, after in scrub_pairs(repo, subject):
        bw, aw = before.split(), after.split()
        for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(None, bw, aw).get_opcodes():
            if tag != "replace" or (i2 - i1) != (j2 - j1):
                continue
            for old, new in zip(bw[i1:i2], aw[j1:j2]):
                old, new = old.strip(".,;:()[]{}\"'`|*#"), new.strip(".,;:()[]{}\"'`|*#")
                if len(old) < 3 or old == new or DOTTED.fullmatch(old):
                    continue
                if not old[:1].isupper():
                    continue  # a name or a place, not a reworded sentence
                if still_at_head(repo, old):
                    continue  # in the tree today, so it was never sensitive
                mapping.setdefault(old, new)
    return mapping


def still_at_head(repo, term):
    return subprocess.run(
        ["git", "-C", repo, "grep", "-I", "-q", "-F", "-e", term, "HEAD"],
        capture_output=True,
    ).returncode == 0


def every_object(repo):
    return subprocess.run(
        ["git", "-C", repo, "cat-file", "--batch-all-objects", "--batch", "--buffer"],
        capture_output=True,
    ).stdout


def derive_addresses(repo, blob=None):
    """Every public address in history, mapped into a documentation range."""
    data = blob if blob is not None else every_object(repo)
    found = {m.decode() for m in DOTTED_BYTES.findall(data)}
    # **The last octet is kept so a reader can still follow a conversation**,
    # but two addresses can share one, as two in this history did, and
    # collapsing them would make history say two hosts were one.
    # A collision falls back to a counter in the same range.
    mapping, taken = {}, set()
    spare = 1
    for ip in sorted(found):
        if ip in NOT_ADDRESSES or not is_public(ip):
            continue
        octet = ip.rsplit(".", 1)[1]
        if octet in taken:
            while str(spare) in taken:
                spare += 1
            octet = str(spare)
        taken.add(octet)
        mapping[ip] = "198.51.100." + octet
    return mapping


def leaky_capture_blobs(repo):
    """Capture blobs carrying a device identity, with their paths."""
    out = {}
    for line in git("rev-list", "--objects", "--all", repo=repo).splitlines():
        parts = line.split(" ", 1)
        if len(parts) != 2 or not parts[1].endswith(".pcap"):
            continue
        sha, path = parts
        data = git("cat-file", "blob", sha, binary=True, repo=repo)
        if IDENTITY.search(data):
            out[sha] = path
    return out


def filter_capture(repo, script, data):
    """Filter one capture's bytes, using the repository's own filter."""
    spec = importlib.util.spec_from_file_location("filter_capture", script)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    with tempfile.TemporaryDirectory() as d:
        src, dst = os.path.join(d, "in.pcap"), os.path.join(d, "out.pcap")
        with open(src, "wb") as f:
            f.write(data)
        module.main(["filter-capture", src, dst, *RADIO_PORTS])
        with open(dst, "rb") as f:
            return f.read()


def verify(repo, mapping):
    """Report what, if anything, is still there. Empty means clean."""
    problems = []
    objects = every_object(repo)
    for old in mapping:
        if old.encode() in objects:
            problems.append(f"{old!r} is still in an object")
        if git("log", "--all", "--format=%h", "--grep", re.escape(old), repo=repo).strip():
            problems.append(f"{old!r} is still in a commit message")
    left = sorted(
        ip
        for ip in {m.decode() for m in DOTTED_BYTES.findall(objects)}
        if is_public(ip) and ip not in NOT_ADDRESSES
    )
    if left:
        problems.append(f"{len(left)} public address(es) remain")
    if leaky_capture_blobs(repo):
        problems.append(f"{len(leaky_capture_blobs(repo))} capture blob(s) still carry an identity")
    return problems


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--repo", default=".")
    p.add_argument("--subject", default=SCRUB_SUBJECT, help="the scrub commit to learn from")
    p.add_argument("--show", action="store_true", help="print the mapping, which names people")
    p.add_argument("--apply", action="store_true", help="rewrite history")
    args = p.parse_args(argv)

    repo = args.repo
    commits = git("rev-list", "--all", "--count", repo=repo).strip()
    words = derive_words(repo, args.subject)
    addresses = derive_addresses(repo)
    mapping = {**words, **addresses}
    captures = leaky_capture_blobs(repo)

    print(f"{commits} commits")
    print(f"  {len(words)} name(s) or town(s), from the scrub commit's own replacements")
    print(f"  {len(addresses)} public address(es), mapped into 198.51.100.x")
    print(f"  {len(captures)} capture blob(s) carrying a device identity")
    if args.show:
        for old, new in mapping.items():
            print(f"    {old} -> {new}")
    elif mapping:
        print("  (--show prints the mapping; it names people, so it is off by default)")

    if not args.apply:
        print("\nnothing changed. --apply rewrites every commit and changes every hash;")
        print("tag and bundle a backup first.")
        return 0

    if not mapping and not captures:
        print("\nnothing to do.")
        return 0

    script = os.path.join(os.path.dirname(os.path.abspath(__file__)), "filter-capture.py")
    substitutes = {
        sha.encode(): filter_capture(repo, script, git("cat-file", "blob", sha, binary=True, repo=repo))
        for sha in captures
    }
    for sha, data in substitutes.items():
        if IDENTITY.search(data):
            print(f"\nrefusing to continue: {sha.decode()[:8]} still carries an identity after filtering")
            return 1

    state = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
    json.dump({"mapping": mapping, "blobs": {k.decode(): v.hex() for k, v in substitutes.items()}}, state)
    state.close()

    callback = f'''
import json
_s = json.load(open({state.name!r}))
_r = [(k.encode(), v.encode()) for k, v in _s["mapping"].items()]
_b = {{k.encode(): bytes.fromhex(v) for k, v in _s["blobs"].items()}}
'''
    blob_cb = callback + '''
sub = _b.get(blob.original_id)
if sub is not None:
    blob.data = sub
else:
    d = blob.data
    for old, new in _r:
        if old in d:
            d = d.replace(old, new)
    blob.data = d
'''
    message_cb = callback + '''
for old, new in _r:
    if old in message:
        message = message.replace(old, new)
return message
'''
    print("\nrewriting...")
    rc = subprocess.run(
        ["git", "-C", repo, "filter-repo", "--force",
         "--blob-callback", blob_cb, "--message-callback", message_cb],
        capture_output=True, text=True,
    )
    os.unlink(state.name)
    if rc.returncode != 0:
        print(rc.stdout[-2000:], rc.stderr[-2000:], sep="\n")
        return rc.returncode

    after = git("rev-list", "--all", "--count", repo=repo).strip()
    print(f"rewritten: {after} commits (was {commits})")
    if after != commits:
        print("commit count changed, which it should not have; check before pushing")

    problems = verify(repo, mapping)
    if problems:
        print("\nVERIFICATION FAILED:")
        for prob in problems:
            print("  " + prob)
        return 1
    print("verified: none of it is in any object, any commit message, or any capture")
    print("\nrun the gates, then push with --force-with-lease when you are ready")
    return 0


if __name__ == "__main__":
    sys.exit(main())
