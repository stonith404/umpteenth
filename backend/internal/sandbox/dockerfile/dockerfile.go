// Package dockerfile inspects job Dockerfiles with BuildKit's parser, which Docker's classic builder uses too, before an adapter builds them
package dockerfile

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
)

// ErrAdd is returned for an ADD instruction, whose URL or git sources the builder fetches itself, outside the egress proxy that build steps go through
// Builds get no context but the Dockerfile, so COPY covers everything else ADD could do
var ErrAdd = errors.New("ADD is not supported, download files in a RUN step instead, e.g. with curl, which goes through the egress proxy")

// Base is an image a FROM instruction builds on
type Base struct {
	Ref string
	// Platform is the platform the FROM names, empty for the builder's own
	Platform string
}

// file is a parsed Dockerfile
type file struct {
	stages   []instructions.Stage
	metaArgs []instructions.ArgCommand
	lex      *shell.Lex
}

func parse(text string) (*file, error) {
	res, err := parser.Parse(strings.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("failed to parse the Dockerfile: %w", err)
	}
	stages, metaArgs, err := instructions.Parse(res.AST, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the Dockerfile: %w", err)
	}
	return &file{stages: stages, metaArgs: metaArgs, lex: shell.NewLex(res.EscapeToken)}, nil
}

// Check rejects source mechanisms whose outbound requests cannot be fully enumerated before building
func Check(text string) error {
	if _, _, _, ok := parser.DetectSyntax([]byte(text)); ok {
		return errors.New("custom Dockerfile frontends are not supported")
	}
	f, err := parse(text)
	if err != nil {
		return err
	}
	for _, stage := range f.stages {
		for _, cmd := range stage.Commands {
			switch c := cmd.(type) {
			case *instructions.AddCommand:
				return ErrAdd
			case *instructions.RunCommand:
				for _, mount := range instructions.GetMounts(c) {
					if mount.From != "" {
						return errors.New("RUN mounts with a from source are not supported")
					}
				}
			case *instructions.OnbuildCommand:
				if err := CheckTriggers([]string{c.Expression}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CheckTriggers rejects deferred downloads and source flags since inherited instructions have no trusted source context
func CheckTriggers(triggers []string) error {
	for _, trigger := range triggers {
		res, err := parser.Parse(strings.NewReader(trigger))
		if err != nil {
			return fmt.Errorf("failed to parse the ONBUILD trigger %q: %w", trigger, err)
		}
		for _, node := range res.AST.Children {
			if strings.EqualFold(node.Value, "add") {
				return fmt.Errorf("an ONBUILD trigger runs %q: %w", trigger, ErrAdd)
			}
			// Deferred instructions inherit no trusted source context, so remote copies and mounts are refused
			if strings.EqualFold(node.Value, "copy") || strings.EqualFold(node.Value, "run") {
				for _, flag := range node.Flags {
					if strings.HasPrefix(strings.ToLower(flag), "--from") || strings.HasPrefix(strings.ToLower(flag), "--mount") {
						return fmt.Errorf("ONBUILD source flags are not supported: %s", trigger)
					}
				}
			}
		}
	}
	return nil
}

// CopyImages returns the images COPY --from instructions copy files out of, leaving out earlier stages named or numbered there
func CopyImages(text string) ([]string, error) {
	f, err := parse(text)
	if err != nil {
		return nil, err
	}
	var images []string
	stages := map[string]bool{}
	for i, stage := range f.stages {
		for _, cmd := range stage.Commands {
			c, ok := cmd.(*instructions.CopyCommand)
			if !ok || c.From == "" {
				continue
			}
			if n, err := strconv.Atoi(c.From); (err == nil && n >= 0 && n < i) || stages[strings.ToLower(c.From)] {
				continue
			}
			images = append(images, c.From)
		}
		if stage.Name != "" {
			stages[strings.ToLower(stage.Name)] = true
		}
	}
	return images, nil
}

// BaseImages returns the images the FROM instructions build on, leaving out earlier stages and scratch
// Names and platforms are expanded with the defaults of the ARGs before the first FROM, which is all the builder expands them with, since builds pass no build arguments of their own
func BaseImages(text string) ([]Base, error) {
	f, err := parse(text)
	if err != nil {
		return nil, err
	}
	env := make([]string, 0, len(f.metaArgs))
	for _, arg := range f.metaArgs {
		for _, kv := range arg.Args {
			env = append(env, kv.Key+"="+kv.ValueString())
		}
	}
	vars := shell.EnvsFromSlice(env)

	var bases []Base
	stages := map[string]bool{}
	for _, stage := range f.stages {
		name, _, err := f.lex.ProcessWord(stage.BaseName, vars)
		if err != nil {
			return nil, fmt.Errorf("failed to expand %q: %w", stage.BaseName, err)
		}
		platform, _, err := f.lex.ProcessWord(stage.Platform, vars)
		if err != nil {
			return nil, fmt.Errorf("failed to expand %q: %w", stage.Platform, err)
		}
		if !stages[strings.ToLower(name)] && name != "scratch" {
			bases = append(bases, Base{Ref: name, Platform: platform})
		}
		if stage.Name != "" {
			stages[strings.ToLower(stage.Name)] = true
		}
	}
	return bases, nil
}

// RewriteSources replaces every external FROM and COPY source with an already imported local image ID
// Replacing whole parsed instruction ranges preserves comments, continuations and heredoc bodies of other instructions
func RewriteSources(text string, resolve func(Base) (string, error)) (string, error) {
	f, err := parse(text)
	if err != nil {
		return "", err
	}
	env := []string{}
	for _, arg := range f.metaArgs {
		for _, kv := range arg.Args {
			env = append(env, kv.Key+"="+kv.ValueString())
		}
	}
	vars := shell.EnvsFromSlice(env)
	lines := strings.Split(text, "\n")
	fromFlag := regexp.MustCompile(`(?i)--from=\S+`)
	type replacement struct {
		start, end int
		text       string
	}
	var replacements []replacement
	stages := map[string]bool{}
	for i, stage := range f.stages {
		base, _, err := f.lex.ProcessWord(stage.BaseName, vars)
		if err != nil {
			return "", err
		}
		platform, _, err := f.lex.ProcessWord(stage.Platform, vars)
		if err != nil {
			return "", err
		}
		if !stages[strings.ToLower(base)] && base != "scratch" {
			local, err := resolve(Base{Ref: base, Platform: platform})
			if err != nil {
				return "", err
			}
			line := "FROM " + local
			if stage.Name != "" {
				line += " AS " + stage.Name
			}
			loc := stage.Location
			replacements = append(replacements, replacement{loc[0].Start.Line - 1, loc[len(loc)-1].End.Line, line})
		}
		for _, cmd := range stage.Commands {
			copy, ok := cmd.(*instructions.CopyCommand)
			if !ok || copy.From == "" {
				continue
			}
			if n, err := strconv.Atoi(copy.From); (err == nil && n >= 0 && n < i) || stages[strings.ToLower(copy.From)] {
				continue
			}
			local, err := resolve(Base{Ref: copy.From, Platform: platform})
			if err != nil {
				return "", err
			}
			loc := copy.Location()
			line := strings.Join(lines[loc[0].Start.Line-1:loc[len(loc)-1].End.Line], "\n")
			match := fromFlag.FindStringIndex(line)
			if match == nil {
				return "", errors.New("unsupported COPY source syntax")
			}
			line = line[:match[0]] + "--from=" + local + line[match[1]:]
			replacements = append(replacements, replacement{loc[0].Start.Line - 1, loc[len(loc)-1].End.Line, line})
		}
		if stage.Name != "" {
			stages[strings.ToLower(stage.Name)] = true
		}
	}
	slices.SortFunc(replacements, func(a, b replacement) int { return b.start - a.start })
	for _, r := range replacements {
		lines = append(append(append([]string{}, lines[:r.start]...), r.text), lines[r.end:]...)
	}
	return strings.Join(lines, "\n"), nil
}
