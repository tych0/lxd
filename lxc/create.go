package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd"
)

type createCmd struct{}

const createUsage = `
lxc create images:ubuntu <name>

Creates a container using the specified image and name
`

func (c *createCmd) usage() string {
	return createUsage
}

func (c *createCmd) flags() {}

func (c *createCmd) run(config *lxd.Config, args []string) error {
	var resourceRef string
	var imageType string
	switch len(args) {
	case 0:
		imageType = config.DefaultImage
		resourceRef = ""
	case 1:
		if strings.HasPrefix(args[0], "images:") {
			imageType = args[0]
			resourceRef = ""
		} else {
			imageType = config.DefaultImage
			resourceRef = args[0]
		}
	case 2:
		imageType = args[0]
		resourceRef = args[1]
	default:
		return errArgs
	}

	fmt.Println(fmt.Sprintf("default image %s, my image %s", config.DefaultImage, imageType))

	// TODO: implement the syntax for supporting other image types/remotes
	if imageType != "images:ubuntu" {
		return fmt.Errorf("images other than images:ubuntu aren't supported right now")
	}

	d, name, err := lxd.NewClient(config, resourceRef)
	if err != nil {
		return err
	}

	l, err := d.Create(name)
	if err == nil {
		fmt.Println(l)
	}
	return err
}
