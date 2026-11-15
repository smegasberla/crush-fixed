# Crush Fixed

**A fork of [Crush](https://github.com/charmbracelet/crush) that spends less per
request.** Same terminal assistant, same sessions, same config — with a much
smaller prompt, transactional edits, and connections that stay warm.

Upstream: **https://github.com/charmbracelet/crush** · This fork:
**https://github.com/smegasberla/crush-fixed**

<p align="center">
    <a href="https://github.com/charmbracelet/crush"><img width="450" alt="Charm Crush Logo" src="https://github.com/user-attachments/assets/cf8ca3ce-8b02-43f0-9d0f-5a331488da4b" /></a>
</p>

## What this fork is

Crush is a terminal coding assistant by [Charm](https://charm.land). It works
well, and it is the starting point here, not the subject. This fork keeps the
UX and changes the cost of a turn: the tokens each request carries, the time
spent before it is sent, and the round trips needed to finish.

It is not a rewrite and not a competitor. If you want the canonical project,
with its releases, its docs and its support, use
**[upstream Crush](https://github.com/charmbracelet/crush)**. Come here if you
want the same agent at a fraction of the prompt.

## Measured results

Taken on one machine, against the same model and the same sessions. Each number
is reproducible from the code in this repository.

| | upstream | this fork |
|---|---|---|
| System prompt per request | 39 444 tokens | **6 941 tokens** (−82 %) |
| Installed skills described in the prompt | 31 269 tokens | 2 600 tokens |
| Stale tool output, 1.2M-token transcript | 1 200 000 tokens re-sent | **201 000 tokens**, nothing dropped |
| Realistic 30-round session, 260k window | 95 000 tokens/request | **61 000 tokens/request** (−35 %) |
| Compaction trigger | 20k below the window edge | 70k tokens, configurable |
| Tool schema payload | 4 447 tokens | 3 631 tokens |

Where the system-prompt saving comes from, in order of size: skill descriptions
are no longer inlined (a bounded index plus a `fetch_skill` loader), the static
guidance sections were rewritten at a third of their length, and the repository
guide is a page instead of six.

## What else changed

- **Provider connections are pooled.** The default transport keeps two idle
  connections per host; an agent exceeds that routinely, so requests re-dialled
  and paid a TLS handshake. One pool is now shared by every provider, sized for
  an agent.
- **Tool results no longer block on language servers.** An edit waited up to 5 s
  for diagnostics and a file read 300 ms; both are sub-second and adaptive. The
  `diagnostics` tool still waits properly when certainty is asked for.
- **Compaction runs at the configured threshold**, not only when the window is
  nearly full, and aged tool output is digested every round rather than at the
  ceiling.
- **Edits are transactional.** One snapshot, `expect`-guarded line addresses,
  all-or-nothing batches, preserved BOM/line endings/final newline/mode, a
  read-back hash of what actually landed, and a per-turn journal you can revert.
- **No attribution is written.** Commits, issues and pull requests carry no
  "Generated with" line and no co-author trailer.
- **A turn is governed, not just looped.** Exploration pressure steers a turn
  that stops making progress, a completion claim with no tool result behind it
  is refused, and duplicate calls are answered from a per-turn cache.
- **Long sessions keep their memory**: project lessons, a plan whose steps need
  evidence to close, and unfinished work offered back at startup.

Every one of these is a setting under `options` in `crush.json`/`crushrc` —
`context_reduction`, `governance`, `edit_safety`, `speed`, `leverage` — and every
default is the measured one.

## Compatibility

Sessions, messages, config and project state keep upstream's format. Switching
between this fork and upstream on the same project reads the same files, so
nothing is lost in either direction. New state (memories, digests, the edit
journal) lives in separate files that upstream simply ignores.

## Install

This fork is built from source: the scripts below clone the repository, build
it, install it, and verify the binary runs. Cloning is not incidental — a
script fetched from a URL has no checkout next to it, so it fetches one itself.

Requires **Go** (`https://go.dev/dl/`) and, when installing from a URL,
**git**.

### macOS and Linux

From a checkout:

```bash
git clone https://github.com/smegasberla/crush-fixed
cd crush-fixed
./scripts/install.sh
```

Or straight from GitHub, with no checkout:

```bash
curl -fsSL https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.sh | bash
```

`curl | bash` cannot take arguments after the pipe, so flags and subcommands are
passed *before* it, through the shell that runs the script:

```bash
# Install under a different name.
curl -fsSL https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.sh | bash -s -- --as crush

# Install into a specific directory.
curl -fsSL https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.sh | bash -s -- --prefix "$HOME/bin"

# Print what would happen, change nothing.
curl -fsSL https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.sh | bash -s -- --dry-run

# Remove it.
curl -fsSL https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.sh | bash -s -- uninstall

# Remove it and its local state (asks before deleting sessions).
curl -fsSL https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.sh | bash -s -- uninstall --purge
```

Everything after `bash -s --` reaches the script as `$1`, `$2`, … — so any
combination works, in any order:

```bash
curl -fsSL https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.sh \
  | bash -s -- --as crush --prefix "$HOME/bin"
```

To build a different branch or tag, set `CRUSH_INSTALL_REF`:

```bash
curl -fsSL https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.sh \
  | CRUSH_INSTALL_REF=some-branch bash
```

### Windows (PowerShell)

From a checkout:

```powershell
git clone https://github.com/smegasberla/crush-fixed
cd crush-fixed
.\scripts\install.ps1
```

Or straight from GitHub:

```powershell
irm https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.ps1 | iex
```

`iex` runs a string, which cannot take parameters, so parameters are passed
through a script block:

```powershell
# Install under a different name.
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.ps1))) -As crush

# Install into a specific directory.
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.ps1))) -Prefix C:\bin

# Print what would happen, change nothing.
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.ps1))) -DryRun

# Remove it.
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.ps1))) -Action uninstall

# Remove it and its local state (prompts before deleting sessions).
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.ps1))) -Action uninstall -Purge
```

A different branch or tag:

```powershell
$env:CRUSH_INSTALL_REF = 'some-branch'
irm https://raw.githubusercontent.com/smegasberla/crush-fixed/main/scripts/install.ps1 | iex
```

### Script flags

| macOS / Linux | Windows | meaning |
|---|---|---|
| `--as NAME` | `-As NAME` | name of the installed binary (default `crush-fixed`) |
| `--prefix DIR` | `-Prefix DIR` | install directory (default `~/.local/bin`, `%LOCALAPPDATA%\Programs\crush-fixed`) |
| `--dry-run` | `-DryRun` | print the plan, change nothing |
| `uninstall` | `-Action uninstall` | remove the binary |
| `--purge` | `-Purge` | with `uninstall`, also delete local state |
| `--help` | — | usage |

Both scripts build from source with `CGO_ENABLED=0`, stamp the version and
commit so `--version` identifies the build, install through a temporary name so
a running binary can be replaced, and **run the installed binary** before
reporting success.

## Getting started

Same as upstream: press <kbd>ctrl+l</kbd> for the model picker, pick a provider,
paste a key. Everything in upstream's documentation applies, apart from the
package-manager installs, which point at the upstream releases rather than this
fork.

- Provider keys: any OpenAI- or Anthropic-compatible endpoint works.
- Config: `crushrc` (Bash with Crush builtins) or `crush.json`, project-local
  or global.
- MCP, LSP, skills, hooks, themes: unchanged.

## Differences at a glance

| | upstream Crush | this fork |
|---|---|---|
| Install | package managers, releases | **build from source** via the scripts above |
| Prompt size | ~39k tokens | **~7k tokens** |
| Skills in the prompt | every description inlined | bounded index + `fetch_skill` loader |
| Edits | plain find/replace | transactional, `expect`-guarded, read-back verified |
| Attribution trailers | on by default | **never written** |
| Session format | — | compatible both ways |

## Building by hand

```bash
go build .
go test ./...
```

`task build`, `task test`, `task lint:fix`, `task fmt` do the same with the
project's task runner. See `AGENTS.md` for the code layout.

## Contributing

This fork tracks upstream. A change that is not about cost, edits or turn
governance belongs upstream: send it to
[charmbracelet/crush](https://github.com/charmbracelet/crush). A change that is
about those belongs here.

## Credits and license

Crush is built by [Charm](https://charm.land) and its contributors; this fork
would not exist without them. Upstream project and documentation:
**https://github.com/charmbracelet/crush**.

Same license as upstream; see [LICENSE](LICENSE).
