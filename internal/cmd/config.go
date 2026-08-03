package cmd

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// configFileName is an optional per-directory config file so users don't have to pass
// --scaffolding-code on every invocation.
const configFileName = ".scaffold.yaml"

// envScaffoldingCode is the environment variable form of --scaffolding-code.
const envScaffoldingCode = "SCAFFOLD_CODE"

type config struct {
	ScaffoldingCode string `yaml:"scaffolding_code"`
}

func loadConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// resolveScaffoldingCodeRoot decides which scaffolding-code path to use, checked in order:
//
//  1. --scaffolding-code=<path>
//  2. $SCAFFOLD_CODE
//  3. scaffolding_code: in ./.scaffold.yaml
//  4. scaffolding_code: in $HOME/.scaffold.yaml
//  5. <directory of the executable>/scaffolding-code
//  6. ./scaffolding-code
//
// Missing or unset config sources are skipped, falling through to the next step.
func resolveScaffoldingCodeRoot(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv(envScaffoldingCode); env != "" {
		return env
	}
	for _, dir := range configSearchDirs() {
		if cfg, err := loadConfig(filepath.Join(dir, configFileName)); err == nil && cfg.ScaffoldingCode != "" {
			return cfg.ScaffoldingCode
		}
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "scaffolding-code")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return defaultScaffoldingCodeRoot
}

// configSearchDirs is the ordered list of directories searched for .scaffold.yaml: the current
// directory first, then the user's home.
func configSearchDirs() []string {
	dirs := []string{"."}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, home)
	}
	return dirs
}
