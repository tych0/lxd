package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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

	resp, err := client.ClusterInit(args[0], "", "", "")
	if err != nil {
		return err
	}

	return client.WaitForSuccess(resp.Operation)
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

	cStatus, err := cc.ServerStatus()
	if err != nil {
		return err
	}

	nStatus, err := nc.ServerStatus()
	if err != nil {
		return err
	}

	nCert, err := shared.ParseCert(nStatus.Environment.Certificate)
	if err != nil {
		return err
	}

	cCert, err := shared.ParseCert(cStatus.Environment.Certificate)
	if err != nil {
		return err
	}

	/* we don't care if the cert was already there */
	cc.CertificateAdd(nCert, args[0])
	nc.CertificateAdd(cCert, args[0])

	addr := ""
	switch a := cStatus.Config["core.https_address"].(type) {
	case string:
		addr = a
	default:
		return fmt.Errorf("core.https_address is not a string: %v", cStatus.Config["core.https_address"])
	}

	resp, err := nc.ClusterInit(name, addr, cStatus.Environment.Certificate, "")
	if err != nil {
		return err
	}

	for i := 0; i < 5; i ++{
		done, err := nc.WaitFor(resp.Operation)
		if err != nil  {
			/* network errors while it's restarting its network,
			 * changing its cert, and waiting to get the new certs
			 * from the DB
			 */
			time.Sleep(1 * time.Second)
			continue
		}

		if done.StatusCode != api.Success {
			return fmt.Errorf("failed cluster init: %s", done.Err)
		}

		return nil
	}

	return fmt.Errorf("cluster didn't come back up after re-loading cert: %s", err)
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
