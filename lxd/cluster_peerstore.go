package main

import (
	"encoding/json"
	//"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"

	rqstore "github.com/rqlite/rqlite/store"
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

	/* We have a special procedure here for when we're not the leader,
	 * since we might have just joined a cluster, so let's not use
	 * clusterDbQuery.
	 */
	result, err := store.Query([]string{"SELECT name, addr FROM cluster_nodes"}, false, true, rqstore.Weak)
	shared.LogErrorf("cluster_nodes result: %s", result)

	if isNotLeaderErr(err) {
		shared.LogErrorf("is not leader in get members")
		var client *lxd.Client

		/*
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
			*/
		leader := store.Leader()
		client, err = connectTo(leader, "")
		if err != nil {
			return nil, err
		}

		info, err := client.ClusterInfo()
		if err != nil {
			return nil, err
		}

		newMembers = info.Members
	} else if err != nil {
		return nil, err
	} else {
		if len(result) != 1 {
			return nil, fmt.Errorf("wrong number of rows, expected %d", len(result))
		}

		rows := result[0]

		if rows.Error != "" {
			return nil, fmt.Errorf(rows.Error)
		}

		for _, r := range rows.Values {
			m := shared.ClusterMember{}

			m.Name = r[0].(string)
			m.Addr = r[1].(string)
			m.Leader = store.Leader() == m.Addr

			newMembers = append(newMembers, m)
		}
	}

	shared.LogErrorf("newMembers: ", newMembers)

	return newMembers, nil
}

func (ps *LXDPeerStore) SetPeers(peers []string) error {
	shared.LogErrorf("SetPeers: %s", peers)

	newMembers, err := ps.getMembers()
	if err != nil {
		shared.LogErrorf("SetPeers err: %s", err)
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
			shared.LogErrorf("SetPeers !found for %s", p)
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
