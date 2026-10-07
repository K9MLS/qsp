#!/usr/bin/env python3
"""Run every console check, or the ones named.

    python3 scripts/console-check/run.py            # all of them
    python3 scripts/console-check/run.py bridges    # one page's

Needs Python 3 with playwright and a Chromium for it, as
scripts/overview-screenshot does:

    pip install playwright && playwright install chromium

Each check is a function named check_... in a file named check_<page>.py.
A failure is listed by that name.
"""
import glob
import importlib
import os
import sys
import traceback

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)


def main(wanted):
    failed, ran = [], 0
    for path in sorted(glob.glob(os.path.join(HERE, "check_*.py"))):
        page = os.path.basename(path)[len("check_"):-len(".py")]
        if wanted and page not in wanted:
            continue
        module = importlib.import_module("check_" + page)
        for name in sorted(n for n in dir(module) if n.startswith("check_")):
            ran += 1
            try:
                getattr(module, name)()
                print("ok    %s: %s" % (page, name))
            except Exception:  # a check failing is the point of running it
                failed.append("%s: %s" % (page, name))
                print("FAIL  %s: %s" % (page, name))
                traceback.print_exc()
    print()
    if not ran:
        print("no checks matched %r" % (wanted,))
        return 1
    if failed:
        print("%d of %d console checks failed:" % (len(failed), ran))
        for name in failed:
            print("  " + name)
        return 1
    print("all %d console checks passed" % ran)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
