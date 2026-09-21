// ctxguard — what your agent's context window is made of, and what it costs.
//
// Measured over 1,295 local Claude Code transcripts: the static startup prefix
// is ~73k tokens and accounts for 20% of ALL cache reads — more than every tool
// result ever returned. This tool makes that visible, per session, per client.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/muthuishere/ctx-guard/internal/adapter"
	"github.com/muthuishere/ctx-guard/internal/install"
)

// Set by goreleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `ctxguard — context measurement for coding agents

  ctxguard status [--client claude]    one-line statusline (reads hook JSON on stdin)
  ctxguard report [--transcript F]     what the context is made of
  ctxguard hook                        hook entrypoint (reads JSON on stdin)
  ctxguard doctor                      what is wired, what is broken, what we cannot see
  ctxguard install [--client all]      wire hooks + statusline + skill (idempotent)
  ctxguard uninstall                   remove exactly what install wrote
  ctxguard version                     version, commit, build date

Clients: claude · codex · opencode · devin
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "status":
		err = cmdStatus(os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	case "hook":
		err = cmdHook(os.Args[2:])
	case "doctor":
		err = cmdDoctor()
	case "install":
		err = cmdInstall(os.Args[2:])
	case "uninstall":
		err = cmdUninstall()
	case "version", "--version", "-v":
		fmt.Println(versionString())
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ctxguard: "+err.Error())
		os.Exit(1)
	}
}

func flag(args []string, name, def string) string {
	for i, a := range args {
		if a == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "--"+name+"=") {
			return strings.TrimPrefix(a, "--"+name+"=")
		}
	}
	return def
}

func stdinBytes() []byte {
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) != 0 {
		return nil // a terminal, not a pipe: don't block waiting for input
	}
	b, _ := io.ReadAll(os.Stdin)
	return b
}

func load(args []string) (adapter.Adapter, adapter.State, adapter.HookInput, error) {
	a := adapter.Get(flag(args, "client", "claude"))
	if a == nil {
		return nil, adapter.State{}, adapter.HookInput{}, fmt.Errorf("unknown client")
	}
	in, _ := a.ParseHook(stdinBytes())
	if tp := flag(args, "transcript", ""); tp != "" {
		in.Transcript = tp
	}
	st, err := a.Read(in)
	return a, st, in, err
}

// dirLabel is the working directory's basename — which repo this session is in.
func dirLabel(in adapter.HookInput) string {
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

func fmtK(n int) string {
	if n >= 100_000 {
		return fmt.Sprintf("%dK", n/1000)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "K"
}

func cmdStatus(args []string) error {
	_, st, in, err := load(args)
	if err != nil {
		return err
	}
	where := ""
	if d := dirLabel(in); d != "" {
		where = d + "  "
	}
	if st.Ctx == 0 {
		fmt.Print(where + "ctx —")
		return nil
	}
	// No window means no percentage. Never invent a denominator.
	if st.Window == 0 {
		fmt.Printf("%sctx %s (window unknown)", where, fmtK(st.Ctx))
		return nil
	}
	pct := float64(st.Ctx) / float64(st.Window)
	flagStr := "🟢"
	if pct >= 0.92 {
		flagStr = "🔴 COMPACT NOW"
	} else if pct >= 0.80 {
		flagStr = "🔴 compact"
	} else if pct >= 0.50 {
		flagStr = "🟡 /clear soon"
	}
	fmt.Printf("%sctx %s %.0f%% %s", where, fmtK(st.Ctx), pct*100, flagStr)
	return nil
}

func cmdReport(args []string) error {
	_, st, _, err := load(args)
	if err != nil {
		return err
	}
	if contains(args, "--json") {
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	win := "(window unknown)"
	if st.Window > 0 {
		win = fmt.Sprintf("(%.0f%% of %s)", float64(st.Ctx)/float64(st.Window)*100, fmtK(st.Window))
	}
	fmt.Printf("context   %s  %s\nmodel     %s\nclient    %s\n\n", fmtK(st.Ctx), win, orNone(st.Model), st.Client)
	if len(st.Prefix) == 0 {
		fmt.Println("no startup prefix recorded in this transcript")
		return nil
	}
	fmt.Println("startup prefix (measured from the transcript, not estimated):")
	yours := 0
	for _, it := range st.Prefix {
		if it.Owner == adapter.OwnerYou {
			yours += it.Tokens
		}
		fmt.Printf("  %-20s %8s   %s\n", it.Label, fmtK(it.Tokens), it.Owner)
	}
	fmt.Printf("  %-20s %8s\n", "recorded", fmtK(st.Recorded))
	fmt.Printf("  %-20s %8s\n", "yours to trim", fmtK(yours))
	fmt.Print("\nBuilt-in tool schemas, full MCP definitions and system-reminder wrappers\n" +
		"are never written to the transcript. They are real and they are large, but\n" +
		"measuring them needs request instrumentation — so they are not attributed here.\n")
	return nil
}

// cmdHook is the UserPromptSubmit entrypoint. It speaks only when crossing into
// a new band, and when it speaks it says what the context is MADE of rather
// than just nagging about the number.
func cmdHook(args []string) error {
	a, st, _, err := load(args)
	if err != nil || st.Ctx == 0 || st.Window == 0 {
		return nil // unknown window: say nothing
	}
	pct := float64(st.Ctx) / float64(st.Window)
	b := 0
	switch {
	case pct >= 0.92:
		b = 3
	case pct >= 0.80:
		b = 2
	case pct >= 0.50:
		b = 1
	}
	if b == 0 || !crossedBand(st.Session, b) {
		return nil
	}
	where := fmt.Sprintf("%s/%.0f%% of %s", fmtK(st.Ctx), pct*100, fmtK(st.Window))
	var msg string
	switch b {
	case 1:
		var parts []string
		for _, it := range st.Prefix {
			if it.Owner == adapter.OwnerYou && it.Tokens > 300 {
				parts = append(parts, fmt.Sprintf("%s %s", it.Label, fmtK(it.Tokens)))
			}
		}
		detail := ""
		if len(parts) > 0 {
			detail = " Yours in the startup prefix: " + strings.Join(parts, " · ") + "."
		}
		msg = "[ctxguard] Context at " + where + "." + detail +
			" Stop re-reading what is already in context; route searches and file dumps through subagents; prefer excerpts over whole files. If the sub-task is done, tell the user to /clear (free). Then answer."
	case 2:
		msg = "[ctxguard] Context at " + where + ". Tell the user: /clear now if the task is done (free), else /compact once (paid). Then answer."
	default:
		msg = "[ctxguard] Context at " + where + " — near the ceiling. Strongly tell the user to /clear or /compact now. Then answer."
	}
	if out := a.EncodeHookOutput("UserPromptSubmit", adapter.Decision{Context: msg}); out != nil {
		os.Stdout.Write(out)
	}
	return nil
}

func cmdDoctor() error {
	fmt.Println("clients:")
	for _, a := range adapter.All() {
		state := "not found"
		if a.Detect() {
			state = "detected"
		}
		fmt.Printf("  %-10s %s\n", a.Name(), state)
	}
	fmt.Print("\nknown limits (verified, not assumed):\n" +
		"  opencode  model context windows unknown -> no percentage is printed\n" +
		"  devin     model context windows unknown -> no percentage is printed\n" +
		"  codex     hooks did NOT fire under `codex exec` across 4 runs; TUI-only unconfirmed\n" +
		"  codex     fails OPEN on hook error — never rely on exit status, emit an explicit decision\n" +
		"  opencode  no subprocess hooks; needs a JS plugin shim calling this binary\n" +
		"  devin     hook config path inferred from binary strings, not verified\n" +
		"  all       ~60% of the window (tool schemas, MCP defs) is never in the transcript\n")
	return nil
}

func cmdInstall(args []string) error {
	dry := contains(args, "--dry-run")
	_, log, err := install.Run(flag(args, "client", "all"), dry)
	if err != nil {
		return err
	}
	if dry {
		fmt.Println("dry run — nothing written:")
	}
	for _, l := range log {
		fmt.Println("  " + l)
	}
	if !dry {
		fmt.Println("\nuninstall restores every file from backup: ctxguard uninstall")
	}
	return nil
}

func cmdUninstall() error {
	log, err := install.Uninstall()
	if err != nil {
		return err
	}
	for _, l := range log {
		fmt.Println("  " + l)
	}
	return nil
}

// versionString prefers goreleaser's ldflags, and falls back to the module
// version the Go toolchain records — otherwise a `go install ...@latest` build
// reports itself as "dev", which is worse than useless in a bug report.
func versionString() string {
	v, c, d := version, commit, date
	if v == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				v = bi.Main.Version
			}
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					if len(s.Value) >= 7 {
						c = s.Value[:7]
					}
				case "vcs.time":
					d = s.Value
				}
			}
		}
	}
	return fmt.Sprintf("ctxguard %s (%s, built %s)", v, c, d)
}

func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
