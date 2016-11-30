package main

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type LXDPeerStore struct {
	members []shared.ClusterMember
}

func NewLXDPeerStore() (*LXDPeerStore, error) {
	ps := LXDPeerStore{members: []shared.ClusterMember{}}

	content, err := ioutil.ReadFile(shared.VarPath("rqlite", "cluster.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("couldn't read peer store: %v", err)
		}

		return &ps, nil
	}

	err = json.Unmarshal(content, &ps.members)
	if err != nil {
		return nil, fmt.Errorf("couldn't unmarshal peer store: %v", err)
	}

	return &ps, nil
}

func (ps *LXDPeerStore) Peers() ([]string, error) {
	peers := []string{}

	for _, p := range ps.members {
		peers = append(peers, p.Addr)
	}

	return peers, nil
}

func (ps *LXDPeerStore) persist() error {
	data, err := json.Marshal(ps.members)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(shared.VarPath("rqlite", "cluster.json"), data, os.FileMode(0600))
	if err != nil {
		return err
	}

	return nil
}

func (ps *LXDPeerStore) getMembers() ([]shared.ClusterMember, error) {
	newMembers := []shared.ClusterMember{}

	rows, err := clusterDbQuery("SELECT name, addr, certificate FROM cluster_members")
	if isNotLeaderErr(err) {
		var client *lxd.Client

		if firstLeaderCert != nil {
			leader := store.Leader()
			serverCertBytes := pem.EncodeToMemory(&pem.Block{
				Type:  "CERTIFICATE",
				Bytes: firstLeaderCert.Raw,
			})
			client, err = connectTo(leader, string(serverCertBytes))
			firstLeaderCert = nil
			if err != nil {
				return nil, err
			}
		} else {
			leader, err := ps.Leader()
			if err != nil {
				return nil, err
			}

			client, err = connectTo(leader.Addr, leader.Certificate)
			if err != nil {
				return nil, err
			}
		}

		info, err := client.ClusterInfo()
		if err != nil {
			return nil, err
		}

		newMembers = info.Members
	} else if err != nil {
		return nil, err
	} else {
		for _, r := range rows.Values {
			m := shared.ClusterMember{}

			n := make([]byte, len(r[0].([]byte)))
			copy(n, r[0].([]byte))
			a := make([]byte, len(r[1].([]byte)))
			copy(a, r[1].([]byte))

			m.Name = string(n)
			m.Addr = string(a)
			m.Certificate = r[2].(string)
			m.Leader = store.Leader() == m.Addr

			newMembers = append(newMembers, m)
		}
	}

	return newMembers, nil
}

func (ps *LXDPeerStore) SetPeers(peers []string) error {
	newMembers, err := ps.getMembers()
	if err != nil {
		return err
	}

	for _, p := range peers {
		found := false

		for _, m := range newMembers {
			if m.Addr == p {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("couldn't find cert info for peer %s", p)
		}
	}

	ps.members = newMembers
	return ps.persist()
}

func (ps *LXDPeerStore) RefreshMembers() error {
	newMembers, err := ps.getMembers()
	if err == nil {
		ps.members = newMembers
	}

	return err
}

func (ps *LXDPeerStore) SetMembers(ms []shared.ClusterMember) error {
	ps.members = ms
	return ps.persist()
}

func (ps *LXDPeerStore) AddPeer(m shared.ClusterMember) error {
	ps.members = append(ps.members, m)
	return ps.persist()
}

func (ps *LXDPeerStore) findClusterMember(cmp func(m shared.ClusterMember) bool) (*shared.ClusterMember, error) {
	for _, m := range ps.members {
		if cmp(m) {
			return &m, nil
		}
	}

	return nil, NoSuchObjectError
}

func (ps *LXDPeerStore) MemberByName(name string) (*shared.ClusterMember, error) {
	return ps.findClusterMember(func(m shared.ClusterMember) bool { return m.Name == name })
}

func (ps *LXDPeerStore) MemberByAddr(addr string) (*shared.ClusterMember, error) {
	return ps.findClusterMember(func(m shared.ClusterMember) bool { return m.Addr == addr })
}

func (ps *LXDPeerStore) Leader() (*shared.ClusterMember, error) {
	return ps.MemberByAddr(store.Leader())
}
