Run a swarm: a fan-out of independent workers that each answer one narrow question and return a short finding.

Use it for a question that can be split into independent lookups: check several subsystems, several files, or several hypotheses. One call runs them all concurrently and returns one combined result, instead of several separate agent calls each carrying their own prompt and report.

Each worker gets a tiny prompt: the shared question plus its own item. It runs on the small model, can only read and search, and is capped at a few steps. Its answer must be short. Costs stay low because workers do not see this conversation and cannot spawn agents of their own.

Pass `items` as one short string per worker. Keep each item a concrete, standalone question; the worker knows nothing else. Use between 2 and 8 items. Results come back in the same order as the items, each labelled with its item.
