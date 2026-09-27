// Command ump is the CLI injected into every sandbox at /usr/local/bin/ump
// It is embedded into the server for linux/amd64 and linux/arm64, so it stays on the standard library and is built with CGO_ENABLED=0
package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// command is one ump subcommand
type command struct {
	// Summary is the one-line description shown by ump help
	Summary string
	// Internal marks plumbing used by the sandbox adapter, which ump help hides from scripts
	Internal bool
	// Run executes the subcommand with the arguments that follow its name and returns the exit code
	Run func(args []string) int
}

// commands is the dispatch table
// Every subcommand lives in its own file and adds itself from an init function, so new subcommands never touch this file
var commands = map[string]command{}

// register adds a subcommand to the dispatch table
func register(name string, c command) {
	if _, ok := commands[name]; ok {
		panic("ump: duplicate command " + name)
	}
	commands[name] = c
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches to a subcommand and returns the process exit code
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}

	name := args[0]
	switch name {
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}

	c, ok := commands[name]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "ump: unknown command %q\n\n", name)
		usage(stderr)
		return 2
	}
	return c.Run(args[1:])
}

// usage prints the public subcommands; internal ones are omitted because scripts must not rely on them
func usage(w io.Writer) {
	names := make([]string, 0, len(commands))
	width := 0
	for name, c := range commands {
		if c.Internal {
			continue
		}
		names = append(names, name)
		width = max(width, len(name))
	}
	slices.Sort(names)

	_, _ = fmt.Fprintln(w, "Usage: ump <command> [arguments]")
	if len(names) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nCommands:")
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "  %s%s  %s\n", name, strings.Repeat(" ", width-len(name)), commands[name].Summary)
	}
}

// errorf prints a prefixed error message to stderr
func errorf(cmd string, format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "ump %s: %s\n", cmd, fmt.Sprintf(format, args...))
}
