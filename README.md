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

Measured on this machine with the shipped defaults. A token estimate uses four
bytes per token; provider billing is the authority when the two differ.

| | before this work | now |
|---|---:|---:|
| System prompt | ~20,900 B (~5,200 tok) | **9,629 B (2,408 tok)** |
| Tool definitions (coder set) | 17,855 B (4,333 tok) | **8,738 B (2,184 tok)**, and bounded by a test |
| Installed skills in the prompt | 7,285 B | **1,965 B** (297 skills installed) |
| First request total | — | **4,687 tokens** |
| One `bash` result ceiling | 30,000 chars (~7,500 tok) | **4,000 chars (~1,000 tok)**, rest spilled to a file |
| One `view` read | 200 KB, 200 lines default | **32 KB, 80 lines default** |
| Compaction trigger / target | 70k / 70k (lands on the trigger) | **180k / 90k** |
| Compaction itself | model call, 30–40 s | **local, milliseconds** |
| Prompt prefix kept across turns | 31% with tool results | **100%** |

Two independent things drive cost: the fixed prompt sent every round, and the
number of rounds. The first is now about 4.6k tokens. The second is handled by
caching: providers reuse an identical prefix from the first token at roughly a
tenth of the price, so compaction is now a rare fallback rather than a routine
event and nothing rewrites an older message just to make it shorter.

## What else changed

- **Compaction is local.** It measures the transcript, keeps a recent verbatim
  tail and stamps a note. No model call. A note recording what the folded part
  did (files read, files changed, commands run, errors seen) is assembled
  locally so the next assistant does not re-read the work. The full transcript
  stays in the database; compaction only moves where the visible window starts.
- **The prompt prefix is byte-stable.** Git state rides on the first user turn,
  governance notes are appended, and duplicate results keep the earliest copy
  so a later turn never rewrites an earlier message. Digesting runs only when
  the prompt exceeds the ceiling.
- **Tool output is capped at source**, with the dropped middle spilled to a file
  whose path stays in the result. Repeated lines are folded with a count while
  distinct lines stay verbatim.
- **Skills load on demand.** A bounded index of 16 described skills plus the
  `fetch_skill` tool; the loader was previously built but never allowlisted.
- **Swarms** fan out independent lookups on the small model, with one shared
  tiny prompt, a read-only tool set, capped findings, no nested workers and a
  per-worker timeout.
- **Provider connections are pooled**, so concurrent agent calls do not re-dial
  and repeat the TLS handshake.
- **Edits are transactional.** One snapshot, `expect`-guarded line addresses,
  all-or-nothing batches, preserved BOM/line endings/final newline/mode, a
  read-back hash, and a per-turn journal you can revert.
- **No attribution is written.** Commits, issues and pull requests carry no
  "Generated with" line and no co-author trailer.
- **A turn is governed, not just looped.** Exploration pressure steers a turn
  that stops making progress, unsupported completion claims are refused, and
  duplicate calls are answered from a per-turn cache.
- **Long sessions keep their memory**: project lessons, a plan whose steps need
  evidence to close, and unfinished work offered back at startup.

Settings live under `options` in `crush.json`/`crushrc`: `context_reduction`,
`governance`, `edit_safety`, `speed`, `leverage`. Key compaction options:

```bash
option compaction-trigger-tokens 180000
option compaction-budget-tokens 90000
model small <provider>/<fast-model-id>
```

The `small` model is what swarms run on. Without one configured, Crush falls
back to a discovered model, which may be the large one.

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

### Themes

Beyond the three upstream themes (`charmtone-panther`, `gruvbox-dark`, `keanu`)
this fork ships nine more, selectable with `active_theme` or the theme switcher:

| Dark | | Light | |
|---|---|---|---|
| `claude` | warm paper and terracotta | `papercut` | black ink on warm paper stock |
| `github` | flat GitHub near-black and blue | `tundra` | cold daylight, slate and steel |
| `abyss` | deep water and bioluminescence | `sakura` | blossom-pink paper and plum ink |
| `ember` | charcoal and the last of a fire | | |
| `nocturne` | indigo room, one gold lamp | | |

The light palettes re-ink their badges and inline code instead of inheriting the
dark defaults, so they stay readable rather than turning into bright-on-bright.

## Differences at a glance

| | upstream Crush | this fork |
|---|---|---|
| Install | package managers, releases | **build from source** via the scripts above |
| First request | — | **4,687 tokens** |
| Compaction | model summary, 30–40 s | local, milliseconds |
| Skills in the prompt | every description inlined | bounded index + `fetch_skill` loader |
| Tool output | unbounded | capped, with a spill pointer |
| Independent lookups | one agent call each | `swarm` fan-out on the small model |
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
