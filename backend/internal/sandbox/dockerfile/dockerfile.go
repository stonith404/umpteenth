// Package dockerfile inspects job Dockerfiles with BuildKit's parser, which Docker's classic builder uses too, before an adapter builds them
package dockerfile

import (
	"errors"
	"fmt"
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

// Check rejects a Dockerfile with an ADD instruction, including one an ONBUILD defers to a later stage
func Check(text string) error {
	f, err := parse(text)
	if err != nil {
		return err
	}
	for _, stage := range f.stages {
		for _, cmd := range stage.Commands {
			switch c := cmd.(type) {
			case *instructions.AddCommand:
				return ErrAdd
			case *instructions.OnbuildCommand:
				if err := CheckTriggers([]string{c.Expression}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CheckTriggers rejects the ONBUILD triggers of a base image when one of them is an ADD, since the builder runs them as the first steps of a stage built on it
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
