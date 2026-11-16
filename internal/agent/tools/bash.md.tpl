Execute shell commands; long-running ones move to background and return a shell ID.
mvdan/sh, so Bash syntax works everywhere; forward slashes in paths.

- Confirm the parent exists before creating a file or directory.
- Dangerous commands ({{ .BannedCommandCount }} built-in, plus config) need approval
  in normal mode, are blocked in YOLO mode, and run unprompted in Sysadmin mode.
  Safe read-only commands always run.
- Prefer the grep/glob/view/ls tools over `find`/`grep`/`cat`/`ls`.
- Chain with `;` or `&&`, no newlines outside quotes. Each call is a fresh shell,
  so state does not persist. Prefer absolute paths over `cd`.
- Plain `sudo`, never `sudo -n`.
{{- if .RgAvailable }}
- `rg` is available and faster than `grep`.
{{- end }}

Background: run_in_background=true returns a shell ID. A job that just started has
printed nothing, so read job_output when its result is next (wait=true blocks).
auto_background_after moves a command to the background after that many seconds.
job_kill ends a job. Use it for servers, watchers and tail -f; not builds, tests or
git. Output over {{ .MaxOutputLength }} characters is truncated, and the marker names a
file holding all of it.

<git_commits>
To commit: in one batch run git status, git diff and git log for style. Stage only
what this change touched. Subject imperative, under 72 characters, saying why not
what; body only for reasoning or tradeoffs, wrapped at 72, via
git commit -m "$(cat <<'EOF' ... EOF)". A failing hook gets one retry, then report
it. Never change git config, push unless asked, or add a "Generated with" line or
co-author trailer. Write for someone reading history without opening files:
"fix: prevent session loading from crashing on missing metadata", not
"fix: nil pointer in session.go".
</git_commits>
