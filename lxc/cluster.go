package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd"
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
	case "create":
		return c.cluster(config, args[1:], true)
	case "join":
		return c.cluster(config, args[1:], false)
	case "leave":
		return c.leave(config, args[1:])
	case "show":
		return c.show(config, args[1:])
	default:
		return errArgs
	}
}

func (c *clusterCmd) cluster(config *lxd.Config, args []string, initFirst bool) error {
	if len(args) < 2 {
		return errArgs
	}

	clients := []*lxd.Client{}
	certs := []*x509.Certificate{}

	for _, remote := range args {
		c, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		status, err := c.ServerStatus()
		if err != nil {
			return err
		}

		clients = append(clients, c)
		certBlock, _ := pem.Decode([]byte(status.Environment.Certificate))
		if certBlock == nil {
			return fmt.Errorf(i18n.G("Invalid certificate"))
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return err
		}

		certs = append(certs, cert)
	}

	/* every server needs to have every other servers' cert */
	for i, c := range clients {
		for _, cert := range certs {
			err := c.CertificateAdd(cert, args[i])
			// XXX: we need a better way to do stuff like this
			if err != nil && err.Error() != "Certificate already in trust store" {
				// TODO: remove the certs that were already added
				return err
			}
		}
	}

	if initFirst {
		err := clients[0].ClusterInit(true)
		if err != nil {
			return err
		}
	}

	addrs := []string{}
	for _, c := range clients[1:] {
		err := c.ClusterInit(false)
		if err != nil {
			return err
		}
		justAddr := strings.TrimPrefix(c.Remote.Addr, "https://")
		addrs = append(addrs, justAddr)
	}

	return clients[0].ClusterAdd(addrs)
}

func (c *clusterCmd) show(config *lxd.Config, args []string) error {
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
	table.SetHeader([]string{"NAME", "URL", "LEADER"})

	for _, m := range info.Members {
		name := "-"
		for k, c := range config.Remotes {
			if strings.TrimPrefix(c.Addr, "https://") == m.Addr {
				name = k
			}
		}

		leader := fmt.Sprintf("%v", m.Leader)
		table.Append([]string{name, m.Addr, leader})
	}

	table.Render()

	return nil
}

func (c *clusterCmd) leave(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	client, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	return client.ClusterRemove(client.Remote.Addr)
}
