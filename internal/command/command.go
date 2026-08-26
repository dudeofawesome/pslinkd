package command

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/dudeofawesome/pslinkd/internal/config"
)

type Run func(config.Config) error

func Execute(args []string, getenv func(string) string, run Run) error {
	if len(args) == 0 || args[0] != "run" {
		return errors.New("usage: pslinkd run [--config PATH]")
	}

	flags := flag.NewFlagSet("pslinkd run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "configuration file path")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("parse command line: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	path := *configPath
	if path == "" {
		var err error
		path, err = config.DefaultPath(getenv, os.UserConfigDir)
		if err != nil {
			return err
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	return run(cfg)
}
