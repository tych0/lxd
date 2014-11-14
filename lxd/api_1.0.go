package main

var commands = []Command{
	pingCmd,
}

func Make10(d *Daemon) {
	v := "1.0"

	for _, cmd := range commands {
		d.handleReq(v, &cmd)
	}

	/*
	mux.HandleFunc("/trust", d.serveTrust)
	mux.HandleFunc("/trust/add", d.serveTrustAdd)
	mux.HandleFunc("/create", d.serveCreate)
	mux.HandleFunc("/shell", d.serveShell)
	mux.HandleFunc("/list", d.serveList)
	mux.HandleFunc("/start", buildByNameServe("start", func(c *lxc.Container) error { return c.Start() }, d))
	mux.HandleFunc("/stop", buildByNameServe("stop", func(c *lxc.Container) error { return c.Stop() }, d))
	mux.HandleFunc("/delete", buildByNameServe("delete", func(c *lxc.Container) error { return c.Destroy() }, d))
	mux.HandleFunc("/restart", buildByNameServe("restart", func(c *lxc.Container) error { return c.Reboot() }, d))
	*/

}
