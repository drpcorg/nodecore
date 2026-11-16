package main

import (
	"bytes"
	"reflect"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
	"gopkg.in/yaml.v3"
)

// The converter builds its output from nodecore's own config structs, so a key that is renamed
// or retyped there is followed here by the compiler instead of by hand. Their yaml tags carry
// omitempty: only what the converter set is written.

// marshalConfig renders a nodecore config as YAML with a two-space indent.
func marshalConfig(cfg *config.AppConfig) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(cfg); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// emptyOptions reports whether no option is set.
func emptyOptions(options *chains.Options) bool {
	return *options == chains.Options{}
}

// mergeOptions overlays later on target: every option set in later replaces the target's. It
// walks the fields instead of naming them, so an option added to nodecore needs no change
// here. Pointer values are copied, never shared.
func mergeOptions(target, later *chains.Options) {
	targetValue, laterValue := reflect.ValueOf(target).Elem(), reflect.ValueOf(later).Elem()
	for i := range laterValue.NumField() {
		value := laterValue.Field(i)
		if value.IsZero() {
			continue
		}
		if value.Kind() == reflect.Pointer {
			copied := reflect.New(value.Type().Elem())
			copied.Elem().Set(value.Elem())
			value = copied
		}
		targetValue.Field(i).Set(value)
	}
}

// cloneOptions returns a copy that shares no pointer with the original.
func cloneOptions(options *chains.Options) *chains.Options {
	copied := new(chains.Options)
	mergeOptions(copied, options)
	return copied
}
