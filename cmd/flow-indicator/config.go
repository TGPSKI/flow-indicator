package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/TGPSKI/flow-indicator/internal/config"
)

// configCmd makes the otherwise implicit XDG configuration discoverable.
func configCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("config: choose init or show")
	}
	switch args[0] {
	case "init":
		return configInit(args[1:])
	case "show":
		return configShow(args[1:])
	default:
		return fmt.Errorf("config: unknown action %q; choose init or show", args[0])
	}
}

func configInit(args []string) error {
	path, err := configCommandPath("config init", args)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config init: create directory for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("config init: %s already exists", path)
		}
		return fmt.Errorf("config init: create %s: %w", path, err)
	}
	defer f.Close()
	if err := writeConfig(f, config.Default()); err != nil {
		return fmt.Errorf("config init: write %s: %w", path, err)
	}
	fmt.Println(path)
	return nil
}

func configShow(args []string) error {
	return configShowTo(os.Stdout, args)
}

func configShowTo(dst io.Writer, args []string) error {
	path, err := configCommandPath("config show", args)
	if err != nil {
		return err
	}
	var cfg config.Config
	if path == "" {
		cfg, err = config.LoadDefaultPath()
	} else {
		cfg, err = config.Load(path)
	}
	if err != nil {
		return err
	}
	return writeConfig(dst, cfg)
}

func configCommandPath(name string, args []string) (string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("config", "", "configuration file path")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("%s: unexpected argument %q", name, fs.Arg(0))
	}
	if *path != "" {
		return *path, nil
	}
	if name == "config show" {
		return "", nil
	}
	return config.Path()
}

func writeConfig(dst io.Writer, cfg config.Config) error {
	enc := json.NewEncoder(dst)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}
