package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The conversion consults the chain registry and nodecore's config validation, which needs
// the method specs loaded - the same order main follows.
func TestMain(m *testing.M) {
	specs_utils.LoadMethodSpecs()
	os.Exit(m.Run())
}

// mustSection parses YAML and fails the test on error.
func mustSection(t *testing.T, yamlText string) *section {
	t.Helper()
	s, err := parseInput([]byte(yamlText))
	require.NoError(t, err)
	return s
}

// reportText returns the report rendered with write(w, 0, 0, "out.yml").
func reportText(t *testing.T, r *report) string {
	t.Helper()
	var out strings.Builder
	r.write(&out, 0, 0, "out.yml")
	return out.String()
}

// strictDecode decodes data into config.AppConfig with unknown keys forbidden, so a key the
// converter misspells fails the test instead of being ignored by nodecore's lenient parser.
func strictDecode(t *testing.T, data []byte) *config.AppConfig {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	parsed := new(config.AppConfig)
	require.NoError(t, decoder.Decode(parsed))
	return parsed
}
