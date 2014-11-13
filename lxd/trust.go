package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/lxc/lxd"
	"code.google.com/p/go.crypto/scrypt"
)


const (
	PW_SALT_BYTES = 32
	PW_HASH_BYTES = 64
)

func (d *Daemon) save_new_password(password string) {
	salt := make([]byte, PW_SALT_BYTES)
	_, err := io.ReadFull(rand.Reader, salt)
	if err != nil {
		lxd.Debugf("failed to get random bytes")
		return
	}

	hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
	if err != nil {
		lxd.Debugf("failed to create hash")
		return
	}

	passfname := lxd.VarPath("adminpwd")
	passOut, err := os.OpenFile(passfname, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	passOut.Write(salt)
	passOut.Write(hash)
	passOut.Close()

}

/*
 * this will need to be made to conform to the rest api.  That
 * switch will come after we get basic certificates supported
 */
func (d *Daemon) serveTrust(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to list")
	if ! d.is_trusted_client(r) {
		lxd.Debugf("Trust request from untrusted client")
		return
	}

	password := r.FormValue("password")
	if password == "" {
		fmt.Fprintf(w, "failed parsing password")
		return
	}

	d.save_new_password(password)
}
