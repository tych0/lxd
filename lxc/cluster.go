package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

type clusterCmd struct{}

func (c *clusterCmd) showByDefault() bool {
	return true
}

func (c *clusterCmd) usage() string {
	return i18n.G(
		`Manage LXD clusters.

lxc cluster create host1 host2`)
}

func (c *clusterCmd) flags() {}

func (c *clusterCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {
	case "enable":
		return c.enable(config, args[1:])
	case "add":
		return c.add(config, args[1:])
	case "remove":
		return c.remove(config, args[1:])
	case "info":
		return c.info(config, args[1:])
	default:
		return errArgs
	}
}

func (c *clusterCmd) enable(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	remote := "local"
	if len(args) == 2 {
		remote = args[0]
	}

	name := args[len(args)-1]

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	return client.ClusterInit(true, name)
}

func (c *clusterCmd) add(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	cluster := args[0]
	node := ""
	if len(args) == 3 {
		node = args[1]
	}

	name := args[len(args)-1]

	cc, err := lxd.NewClient(config, cluster)
	if err != nil {
		return err
	}

	nc, err := lxd.NewClient(config, node)
	if err != nil {
		return err
	}

	nStatus, err := nc.ServerStatus()
	if err != nil {
		return err
	}

	err = nc.ClusterInit(false, name)
	if err != nil {
		return err
	}

	addr := ""
	switch a := nStatus.Config["core.https_address"].(type) {
	case string:
		addr = a
	default:
		return fmt.Errorf("core.https_address is not a string: %v", nStatus.Config["core.https_address"])
	}

	m := shared.ClusterMember{
		Addr:        addr,
		Name:        name,
		Certificate: nStatus.Environment.Certificate,
	}

	return cc.ClusterAdd(m)
}

func (c *clusterCmd) info(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	client, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	info, err := client.ClusterInfo()
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{"NAME", "URL", "LEADER", "STATE"})

	data := [][]string{}
	for _, m := range info.Members {
		name := "-"
		for k, c := range config.Remotes {
			if strings.TrimPrefix(c.Addr, "https://") == m.Addr {
				name = k
			}
		}

		leader := fmt.Sprintf("%v", m.Leader)
		data = append(data, []string{name, m.Addr, leader, "OK"})
	}

	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *clusterCmd) remove(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	client, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	return client.ClusterRemove(client.Remote.Addr)
}
