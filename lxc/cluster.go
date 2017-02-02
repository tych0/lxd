package main

import (
	"fmt"
	"io"
	"os"
	"sort"

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

lxc cluster enable host1`)
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
	case "db":
		return c.db(config, args[1:])
	default:
		return errArgs
	}
}

func (c *clusterCmd) enable(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	client, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	return client.ClusterInit(true, args[0])
}

func (c *clusterCmd) add(config *lxd.Config, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	cluster := args[0]
	node := args[1]
	name := args[1]

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

	cStatus, err := cc.ServerStatus()
	if err != nil {
		return err
	}

	cCert, err := shared.ParseCert(cStatus.Environment.Certificate)
	if err != nil {
		return err
	}

	certAdded := true
	err = nc.CertificateAdd(cCert, args[0])
	if err != nil {
		certAdded = false
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

	err = cc.ClusterAdd(m)
	if err != nil {
		return err
	}

	if false && certAdded {
		err = nc.CertificateRemove(cStatus.Environment.CertificateFingerprint)
		if err != nil {
			return err
		}
	}

	return nil
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
		leader := fmt.Sprintf("%v", m.Leader)
		data = append(data, []string{m.Name, m.Addr, leader, "OK"})
	}

	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *clusterCmd) remove(config *lxd.Config, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	client, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	return client.ClusterRemove(args[1])
}

func (c *clusterCmd) db(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	client, err := lxd.NewClient(config, args[1])
	if err != nil {
		return err
	}

	switch args[0] {
	case "dump":
		rc, err := client.ClusterDBDump()
		if err != nil {
			return err
		}
		defer rc.Close()

		_, err = io.Copy(os.Stdout, rc)
		return err
	case "exec":
		if len(args) != 3 {
			return errArgs
		}

		_, err := client.ClusterDBExecute(args[2])
		return err
	default:
		return errArgs
	}
}
