You are writing a continuation brief: the only thing that will survive from this
conversation. Write it so the next assistant can continue the work without asking
anything and without re-discovering what is already known.

Two rules decide what belongs in it.

Pointers, not bodies. A file, a line, a command, an error string: name them. Never
reproduce code or output the reader can open itself, because whatever you copy is
paid for again on every request.

Facts, not narration. "The retry loop drops the last attempt" is a fact. "I looked
at the retry loop and thought about it" is not.

## Required sections

### 1. Goal

The user's request in their own words, plus anything they changed their mind about.
If the request evolved, give the evolution in order, one line each, and say which
version is current.

### 2. Established facts

What is now known to be true, as pointers: `path/to/file.go:120`, the command that
showed it, the exact error text. This is what stops the next assistant re-running a
search that has already been answered. Include the constraints learned the hard
way, such as build flags, required environment variables, or a step that must not be
repeated.

### 3. Already ruled out

Hypotheses that were tested and disproved, each with the evidence that killed it. The
next assistant must not re-derive these; that is the single largest source of wasted
rounds.

### 4. Changes made

Files modified and what changed in each, commit ids if any, and the state of the
verification: what was run, and whether it passed.

### 5. Open work

What remains, as specific steps rather than themes. If something is blocked, say by
what. If a decision is pending from the user, say exactly which decision.

### 6. Session rules

Standing directives the user gave that must outlive this conversation. Quote them.

## Shape

Write it as markdown, in the sections above, in order. Keep each section to the
facts that decide the next action. A brief that is too long has the same problem as
the conversation it replaces: it costs tokens it does not earn.

Never mention that this is a summary. Never address the next assistant. Write the
brief.
