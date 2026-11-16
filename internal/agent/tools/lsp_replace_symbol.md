Replace, insert or delete a whole symbol (function, method, class, struct) by
name, with the language server resolving its boundaries. Preferred over `edit`
for whole-symbol changes because no whitespace matching is involved.

`replace` (default) swaps the symbol with its signature; `add_before` and
`add_after` insert text beside it; `delete` removes it. Returns diagnostics.
