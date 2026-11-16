Load the full instructions for a skill, by name from the skills index.

An index line is only a trigger: it never contains the procedure, scripts, flags
or reference files, so the skill's behaviour cannot be inferred from its name and
a task that needs it will be done wrong without loading it. Load the skill, then
follow what it says.

Pass `list: true` to see every installed skill with its full description, for when
no index line looked like a match. Loaded skills are marked in that list.
