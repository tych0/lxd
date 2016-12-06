package shared

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

type ClusterMember struct {
	Leader      bool   `json:"leader"`
	Addr        string `json:"addr"`
	Name        string `json:"addr"`
	Certificate string `json:"certificate"`
}

func (c *ClusterMember) ParseCert() (*x509.Certificate, error) {
	certBlock, _ := pem.Decode([]byte(c.Certificate))
	if certBlock == nil {
		return nil, fmt.Errorf("Invalid remote certificate")
	}

	return x509.ParseCertificate(certBlock.Bytes)
}

type ClusterStatus struct {
	Members []ClusterMember `json:"members"`
}
