package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type cliOptions struct {
	repo      string
	workspace string
	resume    bool
	abort     bool
	verbose   bool
}

func main() {
	os.Exit(run())
}

func run() int {
	cmd := newRootCommand(os.Stdout, os.Stderr)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	var opts cliOptions
	cmd := &cobra.Command{
		Use:           "assemble",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssemble(opts, stdout, stderr)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.repo, "repo", ".", "fork repository root")
	flags.StringVar(&opts.workspace, "workspace", "", "assembly workspace")
	flags.BoolVar(&opts.resume, "resume", false, "resume a paused assembly")
	flags.BoolVar(&opts.abort, "abort", false, "abort a paused assembly")
	flags.BoolVar(&opts.verbose, "verbose", false, "log to stderr")
	return cmd
}

func runAssemble(opts cliOptions, stdout, stderr io.Writer) error {
	root, err := filepath.Abs(opts.repo)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return err
	}
	ws := opts.workspace
	if ws == "" {
		ws = filepath.Join(root, ".fork", "work")
	}
	if !filepath.IsAbs(ws) {
		ws, err = filepath.Abs(ws)
		if err != nil {
			return err
		}
	}

	logs := newLogManager(stderr, opts.verbose)
	defer logs.Close()
	logger := logs.Logger()
	lock := NewProcessLock(filepath.Join(root, ".fork", "assemble.lock"), logger)
	if err := lock.Acquire(); err != nil {
		return err
	}
	defer lock.Release()

	git := NewGitExecutor(logger, logs)
	forge, err := NewForgeClient(cfg, git, logger)
	if err != nil {
		return err
	}
	store := NewFileStateStore(filepath.Join(ws, ".git", "fork-assemble-state.json"))
	eng := NewEngine(cfg, ws, store, git, forge, logger)

	var result *RunResult
	switch {
	case opts.abort:
		result, err = eng.Abort()
	case opts.resume:
		logs.AttachWorkspace(ws)
		result, err = eng.Resume()
	default:
		result, err = eng.Start()
	}
	if err != nil {
		return err
	}
	return emitResult(stdout, result)
}

func emitResult(w io.Writer, r *RunResult) error {
	for _, msg := range r.Messages {
		if strings.TrimSpace(msg) != "" {
			fmt.Fprintln(w, msg)
		}
	}
	if r.Kind != resultNone {
		fmt.Fprintf(w, "result=%s\n", r.Kind)
		if strings.TrimSpace(r.Summary) != "" {
			fmt.Fprintln(w, r.Summary)
		}
	}
	if path := os.Getenv("GITHUB_OUTPUT"); path != "" && r.Kind != resultNone {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(f, "result=%s\n", r.Kind)
		if strings.TrimSpace(r.Summary) != "" {
			const delimiter = "EOF_fork_assemble"
			fmt.Fprintf(f, "summary<<%s\n%s\n%s\n", delimiter, r.Summary, delimiter)
		}
	}
	slog.Default().Debug("emitted result", slog.String("result", string(r.Kind)))
	return nil
}
