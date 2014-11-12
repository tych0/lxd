package main

import (
	"net/http"


	"github.com/lxc/lxd"
)

func (d *Daemon) serveTrustAdd(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to trustadd")
	if r.TLS == nil {
		lxd.Debugf("Could not get client certificate")
		return
	}
	lxd.Debugf("tls remote name is %s", r.TLS.ServerName)
	lxd.Debugf("tls peercerts is %s", r.TLS.PeerCertificates)
}
