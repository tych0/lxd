package main

import (
	"fmt"
	"net/http"


	"github.com/lxc/lxd"
)

func (d *Daemon) serveTrustAdd(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to trustadd")

	// TODO - we need to check the admin password first
	if r.TLS == nil {
		lxd.Debugf("Could not get client certificate")
		return
	}
	lxd.Debugf("tls remote name is %s", r.TLS.ServerName)

	for i := range r.TLS.PeerCertificates {
		peercert := &r.TLS.PeerCertificates[i]
		fmt.Printf("PeerCertificate %d : %d\n", i, peercert)
		// TODO: save this certificate
	}
}
