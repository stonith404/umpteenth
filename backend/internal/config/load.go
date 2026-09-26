package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
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
	return load(path, os.Getenv)
}

func load(path string, getenv func(string) string) (*Config, error) {
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
	err := s.applyEnv(getenv)
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
	for _, o := range newSchema(cfg).options {
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
	return cfg
}

// EnvName derives the environment variable of an option from its dotted YAML path, e.g. server.port becomes SERVER_PORT
func EnvName(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// describe names an option in errors by both of the ways it can be set
func describe(key string) string {
	return key + " (" + EnvName(key) + ")"
}

// schema indexes the options of one Config value by their YAML path
type schema struct {
	options  []option
	byKey    map[string]option
	sections map[string]bool
}

// option is a single setting, a leaf of the Config struct tree
type option struct {
	key   string
	field reflect.StructField
	value reflect.Value
}

func newSchema(cfg *Config) *schema {
	s := &schema{byKey: map[string]option{}, sections: map[string]bool{}}
	s.collect(reflect.ValueOf(cfg).Elem(), "")
	return s
}

// collect walks the struct tree and records every nested struct as a section and everything else as an option
func (s *schema) collect(v reflect.Value, prefix string) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}

		key := prefix + name
		if field.Type.Kind() == reflect.Struct {
			s.sections[key] = true
			s.collect(v.Field(i), key+".")
			continue
		}
		o := option{key: key, field: field, value: v.Field(i)}
		s.options = append(s.options, o)
		s.byKey[key] = o
	}
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
		if !s.sections[key] {
			return fmt.Errorf("line %d: unknown option %s", keyNode.Line, key)
		}

		// A section with nothing below it, like "oidc:", sets nothing
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

// applyEnv sets every option whose environment variable, or its _FILE variant for Docker secrets, is set
// An empty variable counts as unset, so a Compose file passing through an unset variable doesn't clear an option of the config file
func (s *schema) applyEnv(getenv func(string) string) error {
	for _, o := range s.options {
		name := EnvName(o.key)
		value, file := getenv(name), getenv(name+"_FILE")

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
