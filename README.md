<h1 align="center">ctx-guard</h1>
<p align="center">What your coding agent's context window is made of — and what it actually costs.</p>

```
ctx-guard  ctx 301K 30% 🟢
```

```
context   301K  (30% of 1000K)
model     claude-opus-5

  system prompt    32.7K   harness
  CLAUDE.md        10.5K   you
  skill roster      8.7K   you
  MCP tool names     1.5K   you
  ─────────────────────────
  recorded         56.2K
  yours to trim    21.5K
```

## The problem

Running a tool is free. Its **output joining the conversation** is not — the whole
conversation is re-sent on every subsequent turn.

```
turn  10        20        30        40        50        60
      │         │         │         │         │         │
      ▼
   read 20k ────────────────────────────────────────────▶  ≈ 1M tokens
      └── paid once ──┘└──── and again, every turn after ────┘
```

Measured across 1,295 local Claude Code transcripts (101 days):

| | |
|---|---|
| cache reads | **36.9B — 97.5% of all tokens** |
| tool output that ever entered context | 27.8M |
| the multiplier | **≈1,300×** |

The bill is `context_length × turn_count`. Not ingestion.

## Where it actually goes

```
static startup prefix   ███████████████████░  7.45B   20% of all cache reads
every tool result ever  ████████████████░░░░  6.42B   17%
  └ images & PDFs       ███████░░░░░░░░░░░░░  2.89B    8%   ← 243 admissions
```

The **preamble costs more than every tool result combined** — and it is the same
~73k tokens in every session, of which **~21k is yours to change**.

## Install

```sh
go install github.com/muthuishere/ctx-guard/cmd/ctxguard@latest
```

Or grab a binary from [releases](https://github.com/muthuishere/ctx-guard/releases) —
macOS, Linux and Windows, x86_64 and arm64:

```sh
tar xzf ctxguard_*.tar.gz && sudo mv ctxguard /usr/local/bin/
ctxguard install
```

No cgo, no runtime dependencies. Reading OpenCode and Devin sessions needs
`sqlite3` on PATH; every other client works without it.

## Use

```bash
ctxguard report     # what the context is made of
ctxguard status     # statusline (reads hook JSON on stdin)
ctxguard doctor     # what is wired — and what we cannot see
ctxguard install    # statusline + hooks for every detected client
ctxguard uninstall  # restore every file from backup
```

## Clients

| | measure | install writes | caveat |
|---|---|---|---|
| **Claude Code** | `message.usage` | `statusLine` + `UserPromptSubmit` | — |
| **Codex** | rollout JSONL — gives `model_context_window` **directly** | `$CODEX_HOME/hooks.json` | hooks did *not* fire under `codex exec` across 4 runs |
| **OpenCode** | `message.data` JSON in `opencode.db` | a JS plugin shim — no subprocess hooks exist | model windows unknown → no % printed |
| **Devin CLI** | `metadata.metrics` **inside** `chat_message` JSON, not in any column | opt-in: `CTXGUARD_DEVIN_HOOKS=<file>` | hook path inferred from binary strings |

## Design rules

Each one comes from a real defect in the version this replaces.

- **Never resolve a path by convention.** Take `transcript_path` / `session_id` from
  the harness; honour `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `OPENCODE_CONFIG_DIR`.
- **Never invent a context window.** Unknown prints no percentage. The old version
  silently fell back to 200K whenever the newest transcript model was `<synthetic>`
  — and so fired its warnings at **5× too low**.
- **Act, don't advise.** A guard costing 677 tokens a session to tell you to save
  tokens is a net loss.
- **Attribute only what is measurable.** ~60% of the window — built-in tool schemas,
  full MCP definitions, `<system-reminder>` wrappers — is never written to any
  transcript.
- **Never rely on exit status.** Codex fails *open* on a hook error; Copilot fails
  *closed*. Always emit an explicit decision object.
- **Be reversible.** Everything written is recorded in `~/.config/ctxguard/install.json`.
  Overwritten files are backed up and restored; created files are deleted. A
  `settings.json` that does not parse is refused, not clobbered.

## Install safety

```
$ ctxguard install
  claude    statusLine + UserPromptSubmit wired (replaced statusline: /my/old/statusline.sh)
  ...
$ ctxguard uninstall
  claude    restored ~/.claude/settings.json      ← byte-identical, old statusline back
  codex     removed  ~/.codex/hooks.json
  opencode  removed  ~/.config/opencode/plugins/ctxguard.js
```
