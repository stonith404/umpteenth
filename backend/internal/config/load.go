package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	// A .env file in the working directory fills the environment before anything reads it
	_ "github.com/joho/godotenv/autoload"
	"go.yaml.in/yaml/v3"
)

// defaultFiles are looked for in the working directory when no config file is given
var defaultFiles = []string{"config.yml", "config.yaml"}

// Load builds the configuration from the schema defaults, the YAML file and the environment, each overriding the one before
// An empty path reads config.yml or config.yaml from the working directory if one exists, since the file is optional
func Load(path string) (*Config, error) {
	return load(path, environ())
}

func load(path string, env map[string]string) (*Config, error) {
	cfg := Default()
	s := newSchema(cfg)

	// Only a config file that was asked for explicitly has to exist
	if path == "" {
		path = findDefaultFile()
	}
	if path != "" {
		err := s.applyFile(path)
		if err != nil {
			return nil, err
		}
	}

	// Environment variables win over the file, so a container can change single options
	err := s.applyEnv(env)
	if err != nil {
		return nil, err
	}

	err = cfg.Validate()
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// Default returns the configuration with only the schema defaults applied
func Default() *Config {
	cfg := &Config{}
	newSchema(cfg).applyDefaults()
	return cfg
}

// environ returns the process environment by variable name
func environ() map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		env[name] = value
	}
	return env
}

// envNameReplacer turns the separators of a YAML path, including the hyphens of collection IDs, into underscores
var envNameReplacer = strings.NewReplacer(".", "_", "-", "_")

// EnvName derives the environment variable of an option from its dotted YAML path, e.g. server.port becomes SERVER_PORT
func EnvName(key string) string {
	return strings.ToUpper(envNameReplacer.Replace(key))
}

// describe names an option in errors by both of the ways it can be set
func describe(key string) string {
	return key + " (" + EnvName(key) + ")"
}

// schema indexes the options of one Config value by their YAML path
type schema struct {
	options     []option
	byKey       map[string]option
	sections    map[string]bool
	collections map[string]collection
}

// option is a single setting, a leaf of the Config struct tree
type option struct {
	key   string
	field reflect.StructField
	value reflect.Value
}

func newSchema(cfg *Config) *schema {
	return newSchemaOf(reflect.ValueOf(cfg).Elem(), "")
}

// newSchemaOf indexes a struct whose options sit below the YAML path prefix
func newSchemaOf(v reflect.Value, prefix string) *schema {
	s := &schema{byKey: map[string]option{}, sections: map[string]bool{}, collections: map[string]collection{}}
	s.collect(v, prefix)
	return s
}

// collect walks the struct tree and records every nested struct as a section, every map of structs as a collection and everything else as an option
func (s *schema) collect(v reflect.Value, prefix string) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}

		key := prefix + name
		switch {
		case field.Type.Kind() == reflect.Struct:
			s.sections[key] = true
			s.collect(v.Field(i), key+".")
		case isCollection(field.Type):
			s.collections[key] = collection{key: key, value: v.Field(i)}
		default:
			o := option{key: key, field: field, value: v.Field(i)}
			s.options = append(s.options, o)
			s.byKey[key] = o
		}
	}
}

// applyDefaults sets every option that has a default tag
func (s *schema) applyDefaults() {
	for _, o := range s.options {
		def, ok := o.field.Tag.Lookup("default")
		if !ok {
			continue
		}
		// A broken default tag is a programming error the unit tests catch
		err := o.set(def)
		if err != nil {
			panic(fmt.Sprintf("invalid default of %s: %v", o.key, err))
		}
	}
}

// collection is a set of sections under IDs the admin chooses, like the sign-in providers under auth.providers
// Its Go type is a map from the ID to a pointer to the section's struct
type collection struct {
	key   string
	value reflect.Value
}

// collectionID keeps IDs usable in URLs and in environment variable names, whose underscores map back to exactly one hyphen
var collectionID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func isCollection(t reflect.Type) bool {
	return t.Kind() == reflect.Map && t.Key().Kind() == reflect.String && t.Elem().Kind() == reflect.Pointer && t.Elem().Elem().Kind() == reflect.Struct
}

// entry returns the schema of the section under the ID, creating the section with its defaults on first use
func (c collection) entry(id string) *schema {
	if c.value.IsNil() {
		c.value.Set(reflect.MakeMap(c.value.Type()))
	}
	mapKey := reflect.ValueOf(id)
	section := c.value.MapIndex(mapKey)
	created := !section.IsValid()
	if created {
		section = reflect.New(c.value.Type().Elem().Elem())
		c.value.SetMapIndex(mapKey, section)
	}

	s := newSchemaOf(section.Elem(), c.key+"."+id+".")
	if created {
		s.applyDefaults()
	}
	return s
}

// ids returns the IDs of every section, sorted so errors come out in a stable order
func (c collection) ids() []string {
	ids := make([]string, 0, c.value.Len())
	for _, k := range c.value.MapKeys() {
		ids = append(ids, k.String())
	}
	slices.Sort(ids)
	return ids
}

// template indexes a detached, empty section, whose option keys are relative to the section
func (c collection) template() *schema {
	return newSchemaOf(reflect.New(c.value.Type().Elem().Elem()).Elem(), "")
}

// envSuffixes returns the environment variable endings of a section's options, like _CLIENT_ID, longest first so an ID never swallows part of an option name
func (c collection) envSuffixes() []string {
	template := c.template()
	suffixes := make([]string, 0, len(template.options))
	for _, o := range template.options {
		suffixes = append(suffixes, "_"+EnvName(o.key))
	}
	slices.SortFunc(suffixes, func(a, b string) int { return len(b) - len(a) })
	return suffixes
}

// findDefaultFile returns the first default config file that exists in the working directory
func findDefaultFile() string {
	for _, name := range defaultFiles {
		if fileExists(name) {
			return name
		}
	}
	return ""
}

// applyFile sets every option the YAML file at path sets
func (s *schema) applyFile(path string) error {
	// #nosec G304 -- the operator chooses the config file
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read the config file: %w", err)
	}

	var doc yaml.Node
	err = yaml.Unmarshal(content, &doc)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}

	// An empty file has no content at all
	if len(doc.Content) == 0 {
		return nil
	}
	err = s.applyMapping(doc.Content[0], "")
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// applyMapping walks one level of the file, descending into sections and setting the options it reaches
// Walking the nodes instead of decoding into the struct lets the file and the environment share one parser, and reports unknown keys with their line
func (s *schema) applyMapping(n *yaml.Node, prefix string) error {
	n = resolveAlias(n)
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: %s must contain options, not a value", n.Line, strings.TrimSuffix(prefix, "."))
	}

	for i := 0; i+1 < len(n.Content); i += 2 {
		keyNode, valueNode := n.Content[i], resolveAlias(n.Content[i+1])
		key := prefix + keyNode.Value

		if o, ok := s.byKey[key]; ok {
			err := o.setNode(valueNode)
			if err != nil {
				return fmt.Errorf("line %d: %s: %w", valueNode.Line, key, err)
			}
			continue
		}
		if c, ok := s.collections[key]; ok {
			err := c.applyMapping(valueNode)
			if err != nil {
				return err
			}
			continue
		}
		if !s.sections[key] {
			return fmt.Errorf("line %d: unknown option %s", keyNode.Line, key)
		}

		// A section with nothing below it, like "auth:", sets nothing
		if valueNode.Tag == "!!null" {
			continue
		}
		err := s.applyMapping(valueNode, key+".")
		if err != nil {
			return err
		}
	}
	return nil
}

// applyMapping adds or changes the section of every ID the mapping lists
func (c collection) applyMapping(n *yaml.Node) error {
	// A collection with nothing below it, like "providers:", adds nothing
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: %s must map IDs to options, not a value", n.Line, c.key)
	}

	for i := 0; i+1 < len(n.Content); i += 2 {
		idNode, valueNode := n.Content[i], resolveAlias(n.Content[i+1])
		id := idNode.Value
		if !collectionID.MatchString(id) {
			return fmt.Errorf("line %d: %s: invalid ID %q, use lowercase letters, digits and single hyphens", idNode.Line, c.key, id)
		}

		entry := c.entry(id)
		if valueNode.Tag == "!!null" {
			continue
		}
		err := entry.applyMapping(valueNode, c.key+"."+id+".")
		if err != nil {
			return err
		}
	}
	return nil
}

// applyEnv creates the section of every ID an environment variable like AUTH_PROVIDERS_POCKET_ID_ISSUER names
// The ID is what the variable has between the collection's prefix and an option name, with its underscores turned back into hyphens
func (c collection) applyEnv(env map[string]string) error {
	prefix := EnvName(c.key) + "_"
	suffixes := c.envSuffixes()
	for _, name := range slices.Sorted(maps.Keys(env)) {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok || env[name] == "" {
			continue
		}

		// The option name is matched on the variable itself first, so only an option that doesn't end in _FILE is read from a file
		envID := ""
		for _, candidate := range []string{rest, strings.TrimSuffix(rest, "_FILE")} {
			for _, suffix := range suffixes {
				if before, found := strings.CutSuffix(candidate, suffix); found && before != "" {
					envID = before
					break
				}
			}
			if envID != "" {
				break
			}
		}
		if envID == "" {
			return fmt.Errorf("%s doesn't end in an option of %s", name, c.key)
		}
		id := strings.ReplaceAll(strings.ToLower(envID), "_", "-")
		if !collectionID.MatchString(id) {
			return fmt.Errorf("%s names the invalid ID %q, use lowercase letters, digits and single hyphens", name, id)
		}
		c.entry(id)
	}

	// Every section then takes its options from the environment, whether the file or a variable created it
	for _, id := range c.ids() {
		err := c.entry(id).applyEnv(env)
		if err != nil {
			return err
		}
	}
	return nil
}

// applyEnv sets every option whose environment variable, or its _FILE variant for Docker secrets, is set
// An empty variable counts as unset, so a Compose file passing through an unset variable doesn't clear an option of the config file
func (s *schema) applyEnv(env map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(s.collections)) {
		err := s.collections[key].applyEnv(env)
		if err != nil {
			return err
		}
	}

	for _, o := range s.options {
		name := EnvName(o.key)
		value, file := env[name], env[name+"_FILE"]

		switch {
		case value != "" && file != "":
			return fmt.Errorf("set either %s or %s_FILE, not both", name, name)
		case file != "":
			// #nosec G304 G703 -- reading the operator-configured _FILE path is the purpose of this option
			content, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("failed to read %s_FILE: %w", name, err)
			}
			value = strings.TrimSpace(string(content))
		case value == "":
			continue
		}

		err := o.set(value)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// setNode sets the option from a value of the config file
func (o option) setNode(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		// An option without a value, like "client_secret:" or "client_secret: \"\"", keeps its default, the same as an empty environment variable
		if n.Tag == "!!null" || n.Value == "" {
			return nil
		}
		return o.set(n.Value)
	case yaml.SequenceNode:
		if o.value.Kind() != reflect.Slice {
			return errors.New("expects a single value, not a list")
		}
		items := make([]string, 0, len(n.Content))
		for _, item := range n.Content {
			item = resolveAlias(item)
			if item.Kind != yaml.ScalarNode {
				return errors.New("expects a list of plain values")
			}
			items = append(items, item.Value)
		}
		o.value.Set(reflect.ValueOf(items).Convert(o.value.Type()))
		return nil
	default:
		return errors.New("expects a value, not a mapping")
	}
}

// set parses a value written the way an environment variable would be, which is also how YAML scalars and default tags are read
func (o option) set(raw string) error {
	v := o.value

	// Durations are int64 underneath, so they are told apart by type before the kinds are
	if v.Type() == reflect.TypeFor[time.Duration]() {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid duration %q, use a value like 30m or 12h", raw)
		}
		v.SetInt(int64(d))
		return nil
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid boolean %q, use true or false", raw)
		}
		v.SetBool(b)
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("invalid whole number %q", raw)
		}
		v.SetInt(int64(n))
	case reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("invalid number %q", raw)
		}
		v.SetFloat(f)
	case reflect.Slice:
		// A list is comma-separated here, while the file can also write it as a YAML list
		items := []string{}
		for item := range strings.SplitSeq(raw, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				items = append(items, item)
			}
		}
		v.Set(reflect.ValueOf(items).Convert(v.Type()))
	default:
		return fmt.Errorf("unsupported option type %s", v.Type())
	}
	return nil
}

// resolveAlias follows a YAML alias like *defaults to the node it points to
func resolveAlias(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// fileExists reports whether a regular file is present at the path
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
