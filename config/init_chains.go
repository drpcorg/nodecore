package main

import (
	_ "embed"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"text/template"
	"unicode"
)

//go:embed public/chains.yaml
var yamlData []byte

type ChainConfig struct {
	ChainSettings ChainSettings `yaml:"chain-settings"`
}

type ChainSettings struct {
	Protocols []Protocol `yaml:"protocols"`
}

type Chain struct {
	ShortNames []string `yaml:"short-names"`
}

type Protocol struct {
	Chains []Chain `yaml:"chains"`
}

const goTemplate = `// Code was generated. DO NOT EDIT.
package chains

type Chain int

const (
{{- range $index, $item := .Items }}
	{{- if eq $index 0 }}
	{{ $item.ConstName }} Chain = iota
	{{- else }}
	{{ $item.ConstName }}
	{{- end }}
{{- end }}
)

var chainsMap = map[string]Chain{
{{- range .Items }}
	"{{ .ShortName }}": {{ .ConstName }},
{{- end }}
}

func (c Chain) String() string {
	switch c {
	{{- range .Items }}
	case {{ .ConstName }}:
		return "{{ .ShortName }}"
	{{- end }}
	default:
		return "Unknown"
	}
}

`

func main() {
	var config ChainConfig
	if err := yaml.Unmarshal(yamlData, &config); err != nil {
		log.Panic().Err(err).Msg("Failed to parse YAML")
	}

	f, err := os.Create("src/chains/chains_data.go")
	if err != nil {
		log.Panic().Err(err).Msg("Failed to create chains.go")
	}
	defer f.Close()

	tmpl, err := template.New("consts").Parse(goTemplate)
	if err != nil {
		log.Panic().Err(err).Msg("Failed to parse template")
	}

	type Const struct {
		ConstName string
		ShortName string
	}

	var items []Const
	for _, item := range config.ChainSettings.Protocols {
		for _, ch := range item.Chains {
			items = append(items, Const{
				ConstName: toConstName(ch.ShortNames[0]),
				ShortName: ch.ShortNames[0],
			})
		}
	}

	if err := tmpl.Execute(f, struct{ Items []Const }{items}); err != nil {
		log.Panic().Err(err).Msg("Failed to execute template:")
	}

	log.Info().Msg("File with chains has been created")
}

func toConstName(s string) string {
	if unicode.IsDigit(rune(s[0])) {
		s = "_" + s
	}
	s = strings.Replace(strings.ToUpper(s), "-", "_", -1)
	return s
}
