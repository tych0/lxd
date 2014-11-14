package main

import (
	"net/http"

	"github.com/lxc/lxd"
)

func pingGet(d *Daemon, w http.ResponseWriter, r *http.Request) {
	remoteAddr := r.RemoteAddr
	if remoteAddr == "@" {
		remoteAddr = "unix socket"
	}
	lxd.Debugf("responding to ping from %s", remoteAddr)

	resp := Jmap{"auth": "guest", "api_compat": lxd.ApiVersion}

	// TODO: When are we "untrusted"? I guess when the client supplies a
	// cert but we don't recognize it?
	if d.is_trusted_client(r) {
		resp["auth"] = "trusted"
		resp["version"] = lxd.Version
	}

	SyncResponse(true, resp, w)
}

var pingCmd = Command{"ping", pingGet, nil, nil}
