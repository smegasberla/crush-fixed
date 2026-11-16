Ask the user a structured question and wait for the answer.

Each item needs `type` (`yes_no`, `single_choice`, `multi_choice`, `free_text`),
a one-line `question`, and a markdown `description` with the context, tradeoffs
or examples (under 300 chars). `yes_no` is accept/reject only; two real
alternatives mean `single_choice` with two choices. `single_choice` and
`multi_choice` need `choices` (max 5) and get a free-text fill-in automatically,
so never add "Other". `free_text` takes no choices.

Max 5 questions per batch. Several questions render as tabs and need
`confirm_title` and `confirm_description`, written as if the answers are known;
an optional 3-word `label` names each tab.

```json
{"questions":[{"label":"Caching","type":"yes_no","question":"Enable caching?",
"description":"Faster repeats, harder invalidation."}],
"confirm_title":"Apply?","confirm_description":"Caching will be enabled."}
```

Use it for destructive or ambiguous actions, requests with several valid
readings, or a choice between named options. Not for anything code or another
tool can answer, and not for permission: that system asks already.
