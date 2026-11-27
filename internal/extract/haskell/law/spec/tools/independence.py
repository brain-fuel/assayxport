#!/usr/bin/env python3
"""S1.j rule I1 (next-cycle-9/design.md, section 6; the vocabulary as next-cycle-10/design.md,
section 8, leaves it), followed through calls.

No function that ax runs may reach a function of a reference, through any
chain of calls, the shared vocabulary apart.

  independence.py [--ax DIR]... [--list] GENERATED.go...

GENERATED.go   the generated Go of the law package (lawspec_typed.go,
               lawspec_fast.go, lawspec_definitions.go), from ax's
               internal/extract/haskell/law or from the test root.
--ax DIR       ax's own sources (.gp): every `LawSpecDefinitions.X` they
               call is a root. Without it the roots are DEFAULT_ROOTS,
               the thirteen definitions ax called at the PR's head.
--list         print the vocabulary that is reached, and by whom.

LawSpec's generator writes above every function the source file and line
of its definition, and merges two definitions with the same body into one
function, which then carries the name of one of them. So a definition of
the implementation that is a copy of a reference's is, here, a call of
the reference, and is found like one.

A function is a REFERENCE when its definition is in haskell_cpp.lawspec,
haskell_policy.lawspec or conditions_policy.lawspec and its name is not
in VOCABULARY below. Adding a name to VOCABULARY is a design decision
(it says: the implementation and its judge may share this), not a fix.
"""
import re, sys, collections

REFERENCE_FILES = ("haskell_cpp.lawspec", "haskell_policy.lawspec", "conditions_policy.lawspec")

# The shared vocabulary: what the implementation may use of the policy
# units as it stands. None of these evaluates an expression or a
# condition, says what a file's directives decide, or reads a source.
# (next cycle 11: `crTerm` joins the never-decided texts; the list of them, `ndList`, and the sentences made of it
#  are the laws' and the README's, and are no vocabulary: the implementation writes a text by its function.)
# (design.md, section 6, has the table with the reason for each group.)
VOCABULARY = {
  "conditions_policy.lawspec": {
    # constants and the never-decided texts
    "buildCap", "unreadText", "unreadableTerm", "fileDefinedTerm", "unexpandedTerm", "ownNameTerm", "headerTerm", "crTerm", "notReadText", "decisionsPath",
    # how a condition is written
    "conditionText", "cubesText", "cubeText", "litText", "simple", "emptyCube", "paired", "depthAfter",
    # an environment's constructors, and the fields of an answer
    "emptyEnv", "completed", "definedAs", "notDefined", "missingOf", "riskyOf", "isBad", "sorted",
    # (next cycle 10, review 12, S2: `envFrom` and `bound` are OUT. They say what a binding line means;
    # the implementation writes that on its own side, and WHEN-7 then compares two witnesses.)
    # the wording of the warnings, of `ax when init` and of the decisions file
    "warningsOf", "warningsWith", "unlisted", "unreadableLines", "unreadLine", "unreadLines", "unreadPaths",
    "unreadEdge", "unreadDecl", "useAt", "useLine", "useLines", "blockOf", "undecidedLine", "packageOf", "macroName",
    "decisionsHeader", "initWarnings", "initNothingText", "bindingErrorText", "shownLine", "lineText", "linesText",
    "dotted", "showValue",
  },
  "haskell_policy.lawspec": {
    # a declaration's record with its defaults; the entry point's mark
    "declOf", "markEntry",
  },
  "haskell_cpp.lawspec": set(),
}

# What ax 58c205c calls of the generated package (haskell.gp, cmd/ax); --ax
# reads the calls from the sources instead.
DEFAULT_ROOTS = ["AssayxportConditionsApplyWhen", "AssayxportConditionsPolicyBuildCap", "CppPlanCapped", "ModuleOf", "PackagesOfCopies",
                 "SkippedPath", "WhenEnv", "WhenHeader", "WhenInitNothing", "WhenInitWarnings", "WhenListed", "WhenPaste", "WhenWarningsWith"]


def lower1(s): return s[:1].lower() + s[1:]

def main(argv):
    ax_dirs, files, listing = [], [], False
    i = 0
    while i < len(argv):
        if argv[i] == "--ax": ax_dirs.append(argv[i + 1]); i += 2
        elif argv[i] == "--list": listing = True; i += 1
        else: files.append(argv[i]); i += 1
    if not files:
        print(__doc__); return 2
    calls, src, name = collections.defaultdict(set), {}, {}
    for f in files:
        cur, note = None, None
        for l in open(f, encoding="utf-8", errors="replace"):
            m = re.match(r"^// (\w+)(?: on logical values)? — laws/(\w+\.lawspec):(\d+)\.", l)
            if m: note = (m.group(1), m.group(2), int(m.group(3))); continue
            m = re.match(r"^func (lsf\w+|lawSpecEvaluate\w+)\(", l) or re.match(r"^func \(lawSpecDefinitions\) (\w+)\(", l)
            if m:
                cur = m.group(1) if l.startswith("func l") else "DEF:" + m.group(1)
                if note: name[cur], src[cur] = note[0], (note[1], note[2])
                note = None
                continue
            if re.match(r"^func ", l): cur = None; note = None; continue
            if cur:
                for c in re.findall(r"\b(lsf[A-Z]\w*|lawSpecEvaluate\w+)\(", l): calls[cur].add(c)
    defined = set(src)
    if not defined:
        print("independence: no generated function with a source note was found in", files); return 2

    def lawspec_name(fn):
        # `// assayxportHaskellCppRan — ...` is the function `ran` of the unit; the
        # generator prefixes a name that two units share. The definition's own
        # name is read from the source note's tail.
        n = name[fn]
        for p in ("assayxportConditionsPolicy", "assayxportHaskellCpp", "assayxportHaskellPolicy",
                  "assayxportHaskellExtract", "assayxportConditions", "assayxportHaskell", "assayxportContract"):
            if n.startswith(p) and len(n) > len(p): return lower1(n[len(p):])
        return n

    def is_reference(fn):
        return fn in src and src[fn][0] in REFERENCE_FILES and re.sub(r"_\d+$", "", lawspec_name(fn)) not in VOCABULARY[src[fn][0]]

    roots = set()
    if ax_dirs:
        import os
        for d in ax_dirs:
            for base, dirs, fs in os.walk(d):
                if base.endswith(os.sep + "law") or (os.sep + "law" + os.sep) in base: continue
                for fn in fs:
                    if not fn.endswith(".gp"): continue
                    for x in re.findall(r"LawSpecDefinitions\.(\w+)", open(os.path.join(base, fn), encoding="utf-8", errors="replace").read()):
                        roots.add("DEF:" + x)
        missing = sorted(r for r in roots if r not in defined)
        if missing: print("independence: ax calls definitions the generated code does not have:", ", ".join(missing)); return 2
    else:
        roots = {"DEF:" + x for x in DEFAULT_ROOTS}
        missing = sorted(r for r in roots if r not in defined)
        if missing: print("independence: the generated code lacks the entry points", ", ".join(missing), "(give --ax)"); return 2
    hits, vocab = {}, collections.defaultdict(set)
    for r in sorted(roots):
        if is_reference(r):
            hits[r] = [r]; continue
        seen, stack = {r}, [(r, [r])]
        while stack:
            fn, path = stack.pop()
            for c in sorted(calls.get(fn, ())):
                if c in seen or c not in defined: continue
                seen.add(c)
                if is_reference(c):
                    hits.setdefault(c, path + [c]); continue
                if src[c][0] in REFERENCE_FILES: vocab[lawspec_name(c)].add(r)
                stack.append((c, path + [c]))
    if listing:
        for v in sorted(vocab): print("vocabulary reached: %-18s from %d root(s), e.g. %s" % (v, len(vocab[v]), sorted(vocab[v])[0]))
    if hits:
        for c in sorted(hits):
            print("%s (%s:%d): %s" % (lawspec_name(c), src[c][0], src[c][1], " -> ".join(hits[c])))
        print("independence: %d function(s) of a reference are reached by what ax runs (%d roots)" % (len(hits), len(roots)))
        return 1
    print("independence: ok (%d roots, %d functions read, %d vocabulary names reached)" % (len(roots), len(defined), len(vocab)))
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
