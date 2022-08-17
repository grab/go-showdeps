// Package cmd is the entrypoint to the go-deps command.
package cmd

import (
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/grab/go-showdeps/config"
	"github.com/grab/go-showdeps/deps"
)

const (
	cfgBasename = ".go-showdeps.yml"
)

// Run starts the command-line application.
func Run() {
	var cfg config.Config

	fset := flag.NewFlagSet("go-showdeps", flag.ContinueOnError)
	fset.StringVar(&cfg.ConfigFile, "config-file", "", "The path of the configuration file")
	fset.StringVar(&cfg.ConfigFile, "c", "", "The path of the configuration file (shorthand)")
	fset.BoolVar(&cfg.StripPath, "strip-path", false, "If set, will strip PathPrefix from package names")
	fset.StringVar(&cfg.PathPrefix, "path-prefix", "", "The prefix to strip from package names. Requires StripPath")
	fset.Usage = cleanenv.FUsage(fset.Output(), &cfg, nil, fset.Usage)

	if err := fset.Parse(os.Args[1:]); err != nil {
		log.Fatalf("couldn't parse command-line args: %v", err)
	}

	cfgFile := cfg.ConfigFile

	// if cfgFile is not specified as a flag, use default paths
	if cfgFile == "" {
		cfgFiles := []string{
			filepath.Join(os.Getenv("HOME"), cfgBasename),
		}

		if pwd, pwdHasErr := os.Getwd(); pwdHasErr == nil {
			cfgFiles = append([]string{filepath.Join(pwd, cfgBasename)}, cfgFiles...)
		}
		for _, fn := range cfgFiles {
			_, err := os.Stat(fn)
			if !errors.Is(err, os.ErrNotExist) {
				cfgFile = fn
			}
		}
	}

	if cfgFile == "" {
		if err := cleanenv.ReadEnv(&cfg); err != nil {
			log.Fatalf("couldn't read config from environment: %v", err)
		}
	} else {
		if err := cleanenv.ReadConfig(cfgFile, &cfg); err != nil {
			log.Fatalf("couldn't read config: %v", err)
		}
	}

	// parse again to override config file / env vars
	if err := fset.Parse(os.Args[1:]); err != nil {
		log.Fatalf("couldn't parse command-line args: %v", err)
	}

	pwd, _ := os.Getwd()
	if err := deps.ShowDeps(pwd, cfg); err != nil {
		log.Fatalf("error showing dependencies: %v", err)
	}
}
