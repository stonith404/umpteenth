package anthropic

import (
	"regexp"
	"strings"
)

// thinkingMode is how a model is asked to think
type thinkingMode int

const (
	// thinkingAdaptive models think adaptively and accept thinking disabled
	thinkingAdaptive thinkingMode = iota
	// thinkingAlwaysOn models always think and reject thinking disabled or budget_tokens with a 400
	thinkingAlwaysOn
	// thinkingBudget models only think with the legacy budget_tokens configuration
	thinkingBudget
	// thinkingUnknown models get no thinking configuration, leaving the API default in place
	thinkingUnknown
)

// traits are the request-shape rules of one Claude model that the catalog's Caps do not capture
type traits struct {
	thinking thinkingMode
	// effort reports whether output_config.effort is accepted
	effort bool
	// forcedToolChoice reports whether tool_choice any/tool is accepted; newer models reject it with a 400
	forcedToolChoice bool
	// maxOutput is the largest max_tokens the model accepts
	maxOutput int
}

// modelTraits follows the claude-api reference: Fable 5.1 and Opus 5.5 always think and reject forced tool use, Haiku 4.5 and older models use budget_tokens and reject effort
var modelTraits = map[string]traits{
	"claude-fable-5-1":  {thinking: thinkingAlwaysOn, effort: true, forcedToolChoice: false, maxOutput: 128_000},
	"claude-mythos-5-1": {thinking: thinkingAlwaysOn, effort: true, forcedToolChoice: false, maxOutput: 128_000},
	"claude-opus-5-5":   {thinking: thinkingAlwaysOn, effort: true, forcedToolChoice: false, maxOutput: 128_000},
	"claude-fable-5":    {thinking: thinkingAlwaysOn, effort: true, forcedToolChoice: true, maxOutput: 128_000},
	"claude-mythos-5":   {thinking: thinkingAlwaysOn, effort: true, forcedToolChoice: true, maxOutput: 128_000},
	"claude-opus-5":     {thinking: thinkingAdaptive, effort: true, forcedToolChoice: true, maxOutput: 128_000},
	"claude-opus-4-8":   {thinking: thinkingAdaptive, effort: true, forcedToolChoice: true, maxOutput: 128_000},
	"claude-opus-4-7":   {thinking: thinkingAdaptive, effort: true, forcedToolChoice: true, maxOutput: 128_000},
	"claude-opus-4-6":   {thinking: thinkingAdaptive, effort: true, forcedToolChoice: true, maxOutput: 128_000},
	"claude-sonnet-5":   {thinking: thinkingAdaptive, effort: true, forcedToolChoice: true, maxOutput: 128_000},
	"claude-sonnet-4-6": {thinking: thinkingAdaptive, effort: true, forcedToolChoice: true, maxOutput: 128_000},
	"claude-haiku-4-5":  {thinking: thinkingBudget, effort: false, forcedToolChoice: true, maxOutput: 64_000},
	"claude-opus-4-5":   {thinking: thinkingBudget, effort: true, forcedToolChoice: true, maxOutput: 64_000},
	"claude-sonnet-4-5": {thinking: thinkingBudget, effort: false, forcedToolChoice: true, maxOutput: 64_000},
	"claude-opus-4-1":   {thinking: thinkingBudget, effort: false, forcedToolChoice: true, maxOutput: 32_000},
}

// unknownTraits is used for models this build does not know, which are most likely newer than it
// Newer models reject forced tool use and some reject explicit thinking settings, so the safe choice is to keep the API's thinking default and steer tool use from the prompt
var unknownTraits = traits{thinking: thinkingUnknown, effort: true, forcedToolChoice: false, maxOutput: 32_000}

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// traitsFor resolves a model ID, including pinned snapshot IDs such as claude-haiku-4-5-20251001
func traitsFor(model string) traits {
	model = strings.TrimSpace(model)
	if t, ok := modelTraits[model]; ok {
		return t
	}
	if t, ok := modelTraits[dateSuffix.ReplaceAllString(model, "")]; ok {
		return t
	}
	return unknownTraits
}
