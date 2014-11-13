package main

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"os"

	"gopkg.in/lxc/go-lxc.v2"

	"code.google.com/p/go.crypto/scrypt"
	"github.com/lxc/lxd"
)

const (
	PW_SALT_BYTES = 32
	PW_HASH_BYTES = 64
)

func save_new_password(password string) {

	salt := make([]byte, PW_SALT_BYTES)
	_, err := io.ReadFull(rand.Reader, salt)
	if err != nil {
		log.Fatal(err)
	}

	hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%x\n", hash)
}

/*
 * this will need to be made to conform to the rest api.  That
 * switch will come after we get basic certificates supported
 */
func (d *Daemon) serveTrust(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to list")
	if !d.is_trusted_client(r.TLS) {
		lxd.Debugf("List request from untrusted client")
	}

	password := r.FormValue("password")
	if password == "" {
		fmt.Fprintf(w, "failed parsing password")
		return
	}

	save_new_password(password)
}
