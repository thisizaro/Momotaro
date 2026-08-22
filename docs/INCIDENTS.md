# Incident log (Momotaro)

**What broke, and what we did about it.** Append-only, newest at the bottom.
Any agent may append directly, this file uses git's `merge=union` driver
(see `.gitattributes`) so concurrent appends merge cleanly.

Log it here when something **actually broke**: a bug that cost real time, a
design assumption that turned out wrong, a deploy or merge that went
sideways, a test that was passing for the wrong reason. This is distinct
from `docs/DECISIONS.md`, which records what we *chose*; this records what
we *got wrong* and what we changed as a result.

Write it while it is fresh. Reconstructing this the night before a demo
produces a list of vague regrets rather than anything useful.

## Why this file exists

Partly engineering hygiene: a team that writes these stops repeating
mistakes. Partly because it is directly assessed. The hackathon's judging
criteria include **"Failure recovery: what broke, and what you did about
it"**, and the honest, specific version of that story is far more convincing
than a claim that nothing went wrong. Nothing going wrong across nine
services means nobody pushed hard enough.

## Format

```markdown
### YYYY-MM-DD, [short title]
**What happened:** [symptom, as observed]
**Root cause:** [what was actually wrong, once known]
**Fix:** [what changed]
**Prevention:** [test added, doc updated, rule changed. "Nothing" is a
valid but suspicious answer.]
```

Keep each one to a few lines. A dozen short honest entries beat two essays.

## Entries

<!-- Append below. Nothing has broken yet, because nothing has been built
     yet. That will change. -->
