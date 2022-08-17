// Package config holds the configuration structs for go-showdeps.
package config

// Config is the top-level config object.
type Config struct {
	ConfigFile string      `yaml:"-" env:"CONFIG"`
	StripPath  bool        `yaml:"strip-path" env:"STRIP_PATH"`
	PathPrefix string      `yaml:"path-prefix" env:"PATH_PREFIX"`
	Rules      []RegexRule `yaml:"rules"`
}

// RegexRule represents a custom regex rule for marking up specific packages.
type RegexRule struct {
	Regex    string `yaml:"regex"`
	Label    string `yaml:"label"`
	Priority int    `yaml:"priority"`
	Color    string `yaml:"color"`
}

