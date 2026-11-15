Execute shell commands; long-running commands automatically move to background and return a shell ID.

<cross_platform>
Uses mvdan/sh interpreter (Bash-compatible on all platforms including Windows).
Use forward slashes for paths: "ls C:/foo/bar" not "ls C:\foo\bar".
Common shell builtins and core utils available on Windows.
</cross_platform>

<execution_steps>
1. Directory Verification: If creating directories/files, use LS tool to verify parent exists
2. Security Check: Dangerous commands ({{ .BannedCommands }}) require explicit user approval with a warning popup in normal mode; YOLO mode blocks them without asking; Sysadmin mode approves them without asking. Safe read-only commands always execute without prompts
3. Command Execution: Execute with proper quoting, capture output
4. Auto-Background: Commands exceeding 1 minute (default, configurable via `auto_background_after`) automatically move to background and return shell ID
5. Output Processing: Truncate if exceeds {{ .MaxOutputLength }} characters. The truncation marker names a file holding the complete output - read it with the view tool when the omitted middle matters
6. Return Result: Include errors, metadata with <cwd></cwd> tags
</execution_steps>

<usage_notes>
- Command required, working_dir optional (defaults to current directory)
- IMPORTANT: Use Grep/Glob/Agent tools instead of 'find'/'grep'. Use View/LS tools instead of 'cat'/'head'/'tail'/'ls'
- Chain with ';' or '&&', avoid newlines except in quoted strings
- Each command runs in independent shell (no state persistence between calls)
- Prefer absolute paths over 'cd' (use 'cd' only if user explicitly requests)
- Run privileged commands with plain `sudo`, never `sudo -n`: the user can approve with Touch ID or another PAM method, and without one sudo fails at once ("a terminal is required") instead of hanging. Each command authenticates separately
{{- if .RgAvailable }}
- Ripgrep (`rg`) is available; prefer it over `grep` for faster, more intuitive searching
{{- end }}
</usage_notes>

<background_execution>
- Set run_in_background=true to run commands in a separate background shell
- Returns a shell ID for managing the background process
- Backgrounding buys you the time the command takes, so spend it: start the job, then keep working
- Do NOT call job_output right after starting a job. It returns the output so far, and a job that just started has printed nothing
- Call job_output once the job's result is the next thing you need, or once you have run out of other work
- job_output with wait=true blocks until the job finishes, giving back the time you just bought; only wait when waiting is the useful thing to do
- A command that auto-backgrounds on the timeout behaves the same way: it is still running, so keep working unless you need its result now
- Use job_kill tool to terminate a background shell
- IMPORTANT: NEVER use `&` at the end of commands to run in background - use run_in_background parameter instead
- Commands that should run in background:
  * Long-running servers (e.g., `npm start`, `python -m http.server`, `node server.js`)
  * Watch/monitoring tasks (e.g., `npm run watch`, `tail -f logfile`)
  * Continuous processes that don't exit on their own
  * Any command expected to run indefinitely
- Commands that should NOT run in background:
  * Build commands (e.g., `npm run build`, `go build`)
  * Test suites (e.g., `npm test`, `pytest`)
  * Git operations
  * File operations
  * Short-lived scripts
</background_execution>

<git_message_quality>
Commit messages are for future readers scanning history. A new contributor reading
only the message must understand what problem this solves, why it matters, and the
impact, without opening files. Avoid code identifiers and file names unless they are
needed to understand the user-facing effect. Bad: "fix: nil pointer in session.go".
Good: "fix: prevent session loading from crashing on missing metadata".
</git_message_quality>

<git_commits>
When the user asks to commit:

1. In one batch: git status, git diff, and git log (for message style).
2. Stage the files this change actually touched; never unrelated ones.
3. Draft a message: first line under 72 characters, imperative verb, "why" not "what".
   Add a body only to explain reasoning or tradeoffs, wrapped at 72 characters.
4. Commit with a HEREDOC and no trailer:
   git commit -m "$(cat <<'EOF'
   <subject>

   <optional body>
   EOF
   )"
5. If a pre-commit hook fails, retry once. If it fails again, the hook is blocking
   the commit: report it instead of forcing.
6. Verify with git status. Never update git config, never push unless asked.
7. Never add a "Generated with" line, a co-author trailer, or any other attribution
   to a commit, issue or pull request.
</git_commits>



<examples>
Good: pytest /foo/bar/tests
Bad: cd /foo/bar && pytest tests
</examples>
