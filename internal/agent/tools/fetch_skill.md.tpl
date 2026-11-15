Load a skill's full instructions by name.

The system prompt lists every installed skill as one line: name, a short
description, and the location. That line is only a trigger — the procedure,
scripts, commands and required flags live in the skill's instructions, and you do
not know what a skill actually does until you have loaded it.

Call this before doing skill-eligible work: if any line in the index matches the
task, load that skill first, then follow what it says.

To see every skill with its full description (for when the index does not name
what you need), pass `list: true`.
