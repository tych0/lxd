package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

var verbose = gnuflag.Bool("v", false, "Enables verbose mode.")
var debug = gnuflag.Bool("debug", false, "Enables debug mode.")
var listenAddr = gnuflag.String("tcp", "", "TCP address to listen on in addition to the unix socket")

func create_cert() error {
	certf := lxd.VarPath("cert.pem")
	keyf := lxd.VarPath("key.pem")
	lxd.Debugf("looking for existing certificates: %s %s", certf, keyf)

	_, err := os.Stat(certf)
	_, err2 := os.Stat(keyf)
	if err == nil && err2 == nil {
		lxd.Debugf("certificates already exist")
		return nil
	}
	if err == nil {
		lxd.Debugf("%s already exists", certf)
		return err2
	}
	if err2 == nil {
		lxd.Debugf("%s already exists", keyf)
		return err
	}

	lxd.Debugf("creating cert: %s %s", certf, keyf)
	return lxd.GenCert(certf, keyf)
}

func run() error {
	gnuflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: lxd [options]\n\nOptions:\n\n    --tcp <addr:port>\n        Bind to addr:port.\n")
		gnuflag.PrintDefaults()
	}

	gnuflag.Parse(true)

	if *verbose || *debug {
		lxd.SetLogger(log.New(os.Stderr, "", log.LstdFlags))
		lxd.SetDebug(*debug)
	}

	err := create_cert()
	if err != nil {
		return err
	}
	lxd.Debugf("created cert")

	d, err := StartDaemon(*listenAddr)
	if err != nil {
		return err
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT)
	signal.Notify(ch, syscall.SIGTERM)
	<-ch
	return d.Stop()
}
