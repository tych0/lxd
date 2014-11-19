package main

import (
	"fmt"
	"github.com/lxc/lxd"
)

type configCmd struct {
	httpAddr string
}

const configUsage = `
Manage configuration.

lxc config set [remote] password <newpwd>        Set admin password
`

func (c *configCmd) usage() string {
	return configUsage
}

func (c *configCmd) flags() {}

func (c *configCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {
	case "set":
		switch args[1] {
		case "password":
			if len(args) != 3 {
				return errArgs
			}

			password := args[2]
			c, _, err := lxd.NewClient(config, "")
			if err != nil {
				return err
			}

			_, err = c.SetRemotePwd(password)
			return err
		case "default-image":
			if len(args) != 3 {
				return errArgs
			}

			config.DefaultImage = args[2]
			return lxd.SaveConfig(*configPath, config)
		}

		return fmt.Errorf("Unknown config item %s", args[1])
	}
	return fmt.Errorf("Unknown action %s", args[0])
}
