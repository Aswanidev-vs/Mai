package models

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateConfigBytes reports YAML keys that Config cannot receive. A silently
// ignored knob is worse than a startup warning: the user tunes it, hears no
// change, and starts doubting the rest of the configuration.
func ValidateConfigBytes(data []byte) []string {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return []string{fmt.Sprintf("config parse error: %v", err)}
	}
	var warnings []string
	if len(root.Content) > 0 {
		walkUnknown(root.Content[0], "", reflect.TypeOf(Config{}), &warnings)
	}
	return warnings
}

func configFields(t reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fields
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if name != "" && name != "-" {
			fields[name] = field.Type
		}
	}
	return fields
}

func walkUnknown(node *yaml.Node, prefix string, t reflect.Type, warnings *[]string) {
	if node.Kind != yaml.MappingNode {
		return
	}
	fields := configFields(t)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		path := key.Value
		if prefix != "" {
			path = prefix + "." + key.Value
		}
		fieldType, ok := fields[key.Value]
		if !ok {
			*warnings = append(*warnings, fmt.Sprintf("ignoring unknown config key %q", path))
			continue
		}
		walkUnknown(value, path, fieldType, warnings)
	}
}
