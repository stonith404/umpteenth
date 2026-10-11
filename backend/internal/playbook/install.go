package playbook

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pipeToShell recognizes a download piped into a shell, which both Dockerfiles and scripts are checked for
var pipeToShell = regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`)

// softwareInstalls recognize steps that install software into the sandbox, which every run would repeat instead of the job's image providing it once
// A project's own dependencies, such as npm ci or pip install -r requirements.txt in a cloned repository, are not software installs
var softwareInstalls = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:apt-get|apt|apk|dnf|yum|microdnf|zypper|brew|conda|mamba|micromamba)["',\s]+(?:-{1,2}[\w=-]+["',\s]+)*(?:install|add)\b`),
	regexp.MustCompile(`(?i)\b(?:cargo|gem)["',\s]+install\b`),
	regexp.MustCompile(`(?i)\bpipx["',\s]+(?:install|run)\b`),
	regexp.MustCompile(`(?i)\bgo["',\s]+install\b[^\n;&|]*@`),
	regexp.MustCompile(`(?i)\buv["',\s]+(?:tool["',\s]+(?:install|run)|python["',\s]+install)\b`),
	regexp.MustCompile(`(?i)(?:^|[\s"'(;&|])uvx["',\s]`),
	regexp.MustCompile(`(?i)\buv["',\s]+run\b[^\n;&|]*--with(?:[\s"'=]|$)`),
	regexp.MustCompile(`(?i)\bnpx["',\s]+(?:-y|--yes)\b`),
	regexp.MustCompile(`(?i)\byarn["',\s]+global["',\s]+add\b`),
	regexp.MustCompile(`(?i)\b(?:nvm|fnm|volta|pyenv|rustup|asdf|mise)["',\s]+(?:toolchain["',\s]+)?install\b`),
	regexp.MustCompile(`(?i)nodejs\.org/(?:dist|download/release)/\S*/node-|\b(?:go\.dev/dl|dl\.google\.com/go|golang\.org/dl|python\.org/ftp/python)/|python-build-standalone|sh\.rustup\.rs|static\.rust-lang\.org|get\.pnpm\.io|bun\.sh/install|deno\.land/install|astral\.sh/uv|nvm-sh/nvm`),
	pipeToShell,
}

var (
	pipInstall  = regexp.MustCompile(`(?i)\bpip(?:3(?:\.\d+)?)?["',\s]+install\b([^\n;&|)]*)`)
	nodeInstall = regexp.MustCompile(`(?i)\b(?:npm|pnpm|bun)((?:["',\s]+-[\w-]+)*)["',\s]+(?:install|i|add)\b([^\n;&|)]*)`)
	// inlineDeps recognizes inline script metadata that lists dependencies, which uv run installs before the script starts
	inlineDeps  = regexp.MustCompile(`(?ms)^#\s*///\s*script\s*$.*?^#\s*dependencies\s*=\s*\[\s*["']`)
	archiveURL  = regexp.MustCompile(`(?i)https?://[^\s"'<>]+\.(?:tar\.(?:gz|xz|bz2|zst)|tgz|txz|zip)\b`)
	pathChange  = regexp.MustCompile(`\bPATH=|["']PATH["']|\$PATH\b|\$\{PATH\}|process\.env\.PATH|os\.pathsep`)
	packageSpec = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9._/@-]*(?:(?:==|>=|<=|~=|!=|>|<)\S+)?$`)
	// redirection is a shell redirect with its target, which would otherwise read like a package name
	redirection = regexp.MustCompile(`(?:^|\s)\d*[<>]+&?\s*\S*`)
	// extras are the optional dependencies after a package or a path, such as .[dev], which hide a local path from the checks once split into tokens
	extras = regexp.MustCompile(`([\w./-])\[[\w,.\s-]*\]`)
	// pipValueFlags take the next argument as their value, such as a requirements file or an index URL
	pipValueFlags = []string{"-r", "--requirement", "-c", "--constraint", "-e", "--editable", "-t", "--target", "--prefix", "--root", "-i", "--index-url", "--extra-index-url", "-f", "--find-links", "-p", "--python"}
)

// maxInstallLine is how much of the line that installs software FindInstall returns
const maxInstallLine = 100

// FindInstall returns the line of a script that installs software at run time, such as pip install requests, or an empty string when it installs nothing
// Full-line comments are skipped, since a script may mention an install it relies on without running it
func FindInstall(script string) string {
	// Comments are dropped, but a shebang stays, since uv run --with in it installs packages on every start
	code := withoutComments(script)

	// System package managers, global tools, version managers and runtime downloads install software however they are called
	for _, re := range softwareInstalls {
		if loc := re.FindStringIndex(code); loc != nil {
			return lineAt(code, loc[0], loc[1])
		}
	}

	// pip installs software when it names a package, while a requirements file or a local path is the project's own
	for _, m := range pipInstall.FindAllStringSubmatchIndex(code, -1) {
		if namesPackage(code[m[2]:m[3]]) {
			return lineAt(code, m[0], m[1])
		}
	}

	// npm and its kin only install software into the sandbox with -g, since a local install may be a project's own work
	for _, m := range nodeInstall.FindAllStringSubmatchIndex(code, -1) {
		if isGlobal(code[m[2]:m[3]], code[m[4]:m[5]]) {
			return lineAt(code, m[0], m[1])
		}
	}

	// Inline script metadata lives in comments, so it is looked for in the whole script
	if loc := inlineDeps.FindStringIndex(script); loc != nil {
		return lineAt(script, loc[1]-1, loc[1])
	}

	// An archive downloaded and put on PATH is a tool installed by hand, such as a release of a CLI
	if loc := archiveURL.FindStringIndex(code); loc != nil && pathChange.MatchString(code) {
		return lineAt(code, loc[0], loc[1])
	}
	return ""
}

// withoutComments drops full-line comments in the styles of shell, Python and JavaScript, keeping shebang lines
func withoutComments(script string) string {
	lines := strings.Split(script, "\n")
	kept := lines[:0]
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") || (strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#!")) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// lineAt returns the line a match between start and end begins on, with whitespace collapsed and clipped to maxInstallLine characters
func lineAt(s string, start, end int) string {
	// A pattern may match the whitespace before a command, which can be the end of the line above
	for start < end-1 && unicode.IsSpace(rune(s[start])) {
		start++
	}
	from := strings.LastIndexByte(s[:start], '\n') + 1
	to := len(s)
	if i := strings.IndexByte(s[start:], '\n'); i >= 0 {
		to = start + i
	}
	line := strings.Join(strings.Fields(s[from:to]), " ")
	if utf8.RuneCountInString(line) > maxInstallLine {
		line = string([]rune(line)[:maxInstallLine]) + "…"
	}
	return line
}

// tokens splits arguments on whitespace and on the quotes, commas and brackets of a list in code
func tokens(args string) []string {
	return strings.FieldsFunc(args, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(`"',[]`, r) })
}

// namesPackage reports whether pip arguments name a package to install, which flags, variables and local paths don't
func namesPackage(args string) bool {
	skip := false
	for _, t := range tokens(extras.ReplaceAllString(redirection.ReplaceAllString(args, " "), "$1")) {
		switch {
		case skip:
			skip = false
		case slices.Contains(pipValueFlags, t):
			skip = true
		case strings.HasPrefix(t, "-"), strings.HasPrefix(t, "$"), strings.HasPrefix(t, "."):
		case strings.Contains(t, "://"):
			// A package can be installed straight from a URL, such as git+https://github.com/owner/repo
			return true
		case strings.ContainsAny(t, `/\`):
		case packageSpec.MatchString(t):
			return true
		}
	}
	return false
}

// isGlobal reports whether npm, pnpm or bun installs into the system rather than into a project
func isGlobal(flags, args string) bool {
	return slices.ContainsFunc(append(tokens(flags), tokens(args)...), func(t string) bool { return t == "-g" || t == "--global" })
}
