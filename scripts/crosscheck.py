#!/usr/bin/env python3
"""Generate gold-standard deobfuscation outputs for the realistic player fixture.

The Go test (extractor/realistic_player_test.go) asserts that our goja engine
reproduces these values byte-for-byte. Producing the gold values with the
*reference* Python yt-dlp JSInterpreter is what makes the test a genuine
cross-validation rather than a self-consistency check.

Run (from the repo root, requires the reference yt-dlp importable):
    python scripts/crosscheck.py

It writes extractor/testdata/realistic_player.expected.json.
"""

import json
import os
import sys

# Make the reference yt-dlp importable. Prefer a sibling checkout at ../yt-dlp,
# otherwise fall back to an installed package.
_HERE = os.path.dirname(os.path.abspath(__file__))
_ROOT = os.path.dirname(_HERE)
for cand in (os.path.join(_ROOT, "..", "yt-dlp"), _ROOT):
    if os.path.isdir(os.path.join(cand, "yt_dlp")):
        sys.path.insert(0, os.path.abspath(cand))
        break

from yt_dlp.jsinterp import JSInterpreter  # noqa: E402

FIXTURE = os.path.join(_ROOT, "extractor", "testdata", "realistic_player.js")
OUT = os.path.join(_ROOT, "extractor", "testdata", "realistic_player.expected.json")


def main() -> int:
    with open(FIXTURE, encoding="utf-8") as f:
        js = f.read()

    interp = JSInterpreter(js)

    # Signature cases. sts is passed as an int on the reference side; our Go
    # engine passes it as a JSON string and goja coerces it numerically, so the
    # two agree on `b % a.length`.
    sig_cases = []
    for sig in ("ABCDEFGH", "xYz0123456789ab", "signature-test-input"):
        for sts in ("16801", "2", "1717429"):
            out = interp.call_function("PLAYER_sig", sig, int(sts))
            sig_cases.append({"sig": sig, "sts": sts, "expected": out})

    # n (throttling) cases.
    nsig_cases = []
    for n in ("n_throttling_token_abc", "abcdef", "quickbrownfox"):
        out = interp.call_function("PLAYER_nsig", n)
        nsig_cases.append({"n": n, "expected": out})

    payload = {
        "fixture": "realistic_player.js",
        "generator": "yt-dlp JSInterpreter %s" % __import__("yt_dlp").version.__version__,
        "signature": sig_cases,
        "nsig": nsig_cases,
    }
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(payload, f, indent=2, ensure_ascii=False)

    print("wrote %s" % OUT)
    print("  signature cases: %d" % len(sig_cases))
    print("  nsig cases:      %d" % len(nsig_cases))
    # sanity echo
    for c in sig_cases[:2]:
        print("  sig %-22r sts=%-8s -> %r" % (c["sig"], c["sts"], c["expected"]))
    for c in nsig_cases[:2]:
        print("  n   %-22r -> %r" % (c["n"], c["expected"]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
