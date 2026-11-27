#!/bin/bash
# S1.j next cycle 11: the sentences of ax's README, and of the comment on
# schema.Symbol.When, that a law's text or a rule's wording stands behind.
# No law reads a README; this does.
#   readme-check.sh ax/README.md [ax/internal/schema/schema.gp [laws/conditions_policy.lawspec]]
# 1. What the evaluator does not read is `wp.notReadText`, to the letter
#    (cycle 8; WHEN-2x holds the text and every example Bad).
# 2. Review 11 (B1, S3, N2, N3): the sentences of cycle 9 on what is marked,
#    on joined lines, on a name the names given define, and on the pragma.
# 3. Review 13, S1 (cycle 11): the texts nothing decides. The README's
#    sentence is `wp.ndText`, the comment's sentence is `wp.ndBrief`, and
#    the README's grammar holds every line of `wp.ndGrammarLines`; WHP-17
#    gives the three to the letter. With the law file as third argument
#    this script also asks that its own copies ARE the texts WHP-17 holds:
#    then the README, the comment, the law and the definitions are one list.
#    The sentences that counted them ("Four ...") must be gone.
# 4. Review 13, N1 and B4: what `--when-complete` does with a name that
#    only an included file defines, and a value written as -0.
flat=$(tr '\n' ' ' < "$1" | tr -s ' ')
rc=0
need() { if ! printf '%s' "$flat" | grep -qF -- "$1"; then echo "readme: missing: $1"; rc=1; fi; }
gone() { if printf '%s' "$flat" | grep -qF -- "$1"; then echo "readme: still there: $1"; rc=1; fi; }
ND_TEXT='These C strings are never decided: `"unread"` (the file has more builds than ax reads; one was read, and no names decide its facts); `("unreadable #if at line N")` (that directive'"'"'s parentheses do not pair, and it cannot be read as an expression); `("#if at line N reads a name the file defines")` (the file'"'"'s own definitions do not decide that directive); `("line N names a macro that is not expanded")` (the build keeps a line ax cannot read as the preprocessor does); `("#if at line N reads a name the preprocessor defines")` (that directive reads one of the preprocessor'"'"'s own names as a value); `("the header is not read past line N and CPP may be on")` (from that line on ax cannot be sure it reads the file'"'"'s header as GHC does, and the text after it names CPP); `("line N holds a carriage return alone")` (a carriage return with no line feed after it ends a line for the preprocessor, and ax does not read lines that way). None is valid in an `#if`, so none can be taken for a value.'
ND_BRIEF='These values are C strings and are never decided: "unread", ("unreadable #if at line N"), ("#if at line N reads a name the file defines"), ("line N names a macro that is not expanded"), ("#if at line N reads a name the preprocessor defines"), ("the header is not read past line N and CPP may be on"), ("line N holds a carriage return alone").'
ND_GRAMMAR=("'\"unreadable #if at line ' N '\"'" "'\"#if at line ' N ' reads a name the file defines\"'" "'\"line ' N ' names a macro that is not expanded\"'" "'\"#if at line ' N ' reads a name the preprocessor defines\"'" "'\"the header is not read past line ' N ' and CPP may be on\"'" "'\"line ' N ' holds a carriage return alone\"'")
need "Beyond errors the preprocessor would also report, ax does not read: a character constant of several characters; a character constant with an octal or hexadecimal escape (but '\\0'); a character constant with an escape C does not define; a character constant with a prefix (L, u, U, u8); an integer literal past 2^64 - 1; a call of a macro other than MIN_VERSION_<package> and MIN_VERSION_GLASGOW_HASKELL."
need "A macro with an argument list is never expanded. A build is marked where a line it keeps, after the plain macros are replaced, holds such a macro's name before a \`(\` or as the last thing of a line that is not the file's last"
need "A line that ends in a backslash is joined with the next one before anything else is read"
need "A name that your \`--when\` values or decisions file define marks the build that reads it in a line of code"
need "A file is preprocessed when it has a directive line or the pragma \`LANGUAGE CPP\`"
gone "that no directive of the file names, is not replaced in code"
gone "in a build that calls one, ax reads nothing as certain"
need "$ND_TEXT"
for g in "${ND_GRAMMAR[@]}"; do need "| $g"; done
gone "Four C strings are never decided"
need "Included files are not read: a name that only an \`#include\`d file defines is undefined to ax, and with \`--when-complete\` a directive that tests it is decided as if no build defined it."
need "A value that is zero is not written with a minus sign: \`N=-0\` is a line that cannot be read."
if [ -n "$2" ]; then
  # the comment, its `//` and line breaks taken out
  flat=$(sed 's|^[[:space:]]*//[[:space:]]\{0,1\}||' "$2" | tr '\n' ' ' | tr -s ' ')
  need "$ND_BRIEF"
  gone "Four values are C strings"
fi
if [ -n "$3" ]; then
  # this script's copies are the texts of WHP-17 (a law file writes \" for ")
  flat=$(sed 's/\\"/"/g' "$3" | tr '\n' ' ' | tr -s ' ')
  need "= \"$ND_TEXT\""
  need "= \"$ND_BRIEF\""
  for g in "${ND_GRAMMAR[@]}"; do need "\"$g\""; done
fi
[ $rc = 0 ] && echo "readme: ok"
exit $rc
