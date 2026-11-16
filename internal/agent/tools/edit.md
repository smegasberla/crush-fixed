Find-and-replace within a file; can also create or delete content. A match that
differs only in whitespace still edits, with new_string re-indented, and the
response says so. Prefer `lsp_replace_symbol` for whole functions and
`lsp_rename` for renames; use write for large rewrites.
