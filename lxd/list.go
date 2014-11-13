package main

import (
	"fmt"
	"net/http"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
)

func (d *Daemon) serveList(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to list")
	if r.TLS == nil {
		return
	}
	for i := range r.TLS.PeerCertificates {
		if d.CheckTrustState(*r.TLS.PeerCertificates[i]) {
			lxd.Debugf("cert is good!")
		} else {
			lxd.Debugf("cert is not saved!")
		}
	}

	c := lxc.DefinedContainers(d.lxcpath)
	for i := range c {
		fmt.Fprintf(w, "%d: %s (%s)\n", i, c[i].Name(), c[i].State())
	}
}
