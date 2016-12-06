package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"time"

	"github.com/hashicorp/raft"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"

	rqhttp "github.com/rqlite/rqlite/http"
	rqstore "github.com/rqlite/rqlite/store"
	rqdb "github.com/rqlite/rqlite/db"
)

const CLUSTER_SCHEMA string = `
CREATE TABLE IF NOT EXISTS cluster_members (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    addr VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (addr),
    UNIQUE (name)
);
`

var (
	transport *LXDTransport
	store     *rqstore.Store
	service   *rqhttp.Service
	members   []shared.ClusterMember
)

func ClusterMode() bool {
	return transport != nil
}

// LXDTransport basically wraps our websocket API, creating a transport layer
// on top of it for raft to use.
type LXDTransport struct {
	myAddr string
	queue  chan(net.Conn)
	stop   chan(bool)
}

// implement net.Listener
func (t *LXDTransport) Accept() (net.Conn, error) {
	select {
	case <-t.stop:
		close(t.queue)
		return nil, fmt.Errorf("disconnected")
	case next := <-t.queue:
		return next, nil
	}
}

func (t *LXDTransport) Close() error {
	t.stop <- true
	return nil
}

type lxdAddr struct {
	myAddr string
}

func (l *lxdAddr) Network() string {
	return "tcp"
}

func (l *lxdAddr) String() string {
	return l.myAddr
}

func (t *LXDTransport) Addr() net.Addr {
	return &lxdAddr{t.myAddr}
}

func (t *LXDTransport) Dial(address string, timeout time.Duration) (net.Conn, error) {
	config, err := shared.GetTLSConfig("", "", "", nil)
	if err != nil {
		return nil, err
	}

	// XXX: TODO: we could figure out the right cert since we have it in
	// our trust store
	config.InsecureSkipVerify = true

	dialer := websocket.Dialer{
		TLSClientConfig: config,
		NetDial:         shared.RFC3493Dialer,
	}

	url := fmt.Sprintf("wss://%s/1.0/cluster/connect", address)
	conn, _, err := dialer.Dial(url, http.Header{})
	if err != nil {
		return nil, err
	}

	return &WebsocketToNetConn{conn: conn}, nil
}

func (d *Daemon) ClusterAddr() (string, error) {
	addrStr := d.TCPSocket.Socket.Addr().String()
	justAddr, _, err := net.SplitHostPort(addrStr)
	if err != nil {
		return "", err
	}

	addr := net.ParseIP(justAddr)
	if addr == nil {
		return "", fmt.Errorf("unparsable ip %s", addrStr)
	}

	if addr.IsUnspecified() {
		return "", fmt.Errorf("Cannot use wildcard addr %s as cluster addr", addrStr)
	}

	return addrStr, nil
}

func StartRQLite(d *Daemon, leader bool) error {
	myAddr, err := d.ClusterAddr()
	if err != nil {
		return err
	}

	shared.LogInfof("starting rqlite on %s", myAddr)

	config := rqstore.NewDBConfig("", true)
	transport = &LXDTransport{
		myAddr: myAddr,
		queue:  make(chan net.Conn, 10),
		stop:   make(chan bool),
	}

	store = rqstore.New(config, shared.VarPath("rqlite"), transport)

	err = store.Open(leader)
	if err != nil {
		store.Close(false)
		store = nil
		return err
	}

	// XXX: no real way to hook the rqlite's logger to redirect it
	// to ours; we need to do something fancier here.
	service = rqhttp.New(myAddr, store, nil)

	// rqlite expects this to be populated
	service.BuildInfo = map[string]interface{}{
		"commit":     "unknown",
		"branch":     "unknown",
		"version":    "LXD" + shared.Version,
		"build_time": "unknown",
	}

	return nil
}

// /1.0/cluster/internal
func ClusterHandler(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = r.URL.Path[len("/1.0/cluster/internal"):]

	service.ServeHTTP(w, r)
}

// /1.0/cluster/connect
type clusterConnResponse struct {
	req *http.Request
}

func (r *clusterConnResponse) Render(w http.ResponseWriter) error {
	if transport == nil {
		return fmt.Errorf("clustering not enabled")
	}

	conn, err := shared.WebsocketUpgrader.Upgrade(w, r.req, nil)
	if err != nil {
		return err
	}

	transport.queue <- &WebsocketToNetConn{conn: conn}
	return nil
}

func (r *clusterConnResponse) String() string {
	return "cluster connection"
}

func clusterConnectGet(d *Daemon, r *http.Request) Response {
	return &clusterConnResponse{r}
}

// XXX: client code should (?) already coordinate this
var clusterConnectCmd = Command{name: "cluster/connect", get: clusterConnectGet, untrustedGet: true}

// /1.0/cluster
func clusterGet(d *Daemon, r *http.Request) Response {
	state := "DISABLED"
	if ClusterMode() {
		state = "OK"
	}

	ret := map[string]interface{}{
		"state": state,
		"keys": []string{},
	}

	return SyncResponse(true, ret)
}

func clusterPost(d *Daemon, r *http.Request) Response {
	leader := r.FormValue("leader")
	name := r.FormValue("name")

	err := StartRQLite(d, leader == "true")
	if err != nil {
		return InternalError(err)
	}

	if leader == "true" {
		_, err := store.WaitForLeader(5 * time.Second)
		if err != nil {
			transport = nil
			store.Close(false)
			store = nil
			service = nil
			return InternalError(err)
		}

		cert, err := ioutil.ReadFile(shared.VarPath("server.crt"))
		if err != nil {
			transport = nil
			store.Close(false)
			store = nil
			service = nil
			return InternalError(err)
		}

		addr, err := d.ClusterAddr()
		if err != nil {
			transport = nil
			store.Close(false)
			store = nil
			service = nil
			return InternalError(err)
		}

		me := addMemberStmt(addr, name, string(cert))

		_, err = store.Execute([]string{CLUSTER_SCHEMA, me}, false, false)
		if err != nil {
			transport = nil
			store.Close(false)
			store = nil
			service = nil
			return InternalError(err)
		}
	}

	return EmptySyncResponse
}

var clusterCmd = Command{
	name: "cluster",
	get: clusterGet,
	post: clusterPost,
}

// /1.0/cluster/nodes
func clusterNodesGet(d *Daemon, r *http.Request) Response {
	leader := store.Leader()

	ret, err := ClusterInfo()
	if err != nil {
		return InternalError(err)
	}

	for _, n := range ret {
		if n.Addr == leader {
			n.Leader = true
			break
		}
	}

	return SyncResponse(true, shared.ClusterStatus{ret})
}

func clusterNodesPost(d *Daemon, r *http.Request) Response {
	m := shared.ClusterMember{}

	err := json.NewDecoder(r.Body).Decode(&m)
	if err != nil {
		return BadRequest(err)
	}

	_, err = store.Execute([]string{addMemberStmt(m.Addr, m.Name, m.Certificate)}, false, true)
	if err != nil {
		return InternalError(err)
	}

	// XXX: need to guarantee that this is the leader or proxy the request
	err = store.Join(m.Addr)
	if err != nil {
		// XXX: see note elsewhere about prepared statements
		_, err := store.Execute([]string{fmt.Sprintf("DELETE FROM cluster_members WHERE name = '%s'", m.Name)}, false, true)
		if err != nil {
			return InternalError(err)
		}
		return InternalError(err)
	}

	return EmptySyncResponse
}

var clusterNodesCmd = Command{
	name: "cluster/nodes",
	get: clusterNodesGet,
	post: clusterNodesPost,
}

func clusterDelete(d *Daemon, r *http.Request) Response {
	addr := r.FormValue("addr")

	err := store.Remove(addr)
	if err != nil {
		shared.LogErrorf("result: %v %#v", err, err)
		if err == rqstore.ErrNotLeader || err == raft.ErrNotLeader {
			leader := store.Leader()
			if leader == "" {
				return ServiceUnavailable(fmt.Errorf("there is no leader; try again later"))
			}

			cert, err := ioutil.ReadFile(shared.VarPath("server.crt"))
			if err != nil {
				return InternalError(err)
			}

			key, err := ioutil.ReadFile(shared.VarPath("server.key"))
			if err != nil {
				return InternalError(err)
			}

			l, err := lxd.NewClientFromInfo(lxd.ConnectInfo{
				Name: leader,
				RemoteConfig: lxd.RemoteConfig{
					Addr: leader,
				},
				ClientPEMCert: string(cert),
				ClientPEMKey: string(key),
			})
			if err != nil {
				return InternalError(err)
			}

			err = l.ClusterRemove(addr)
			if err != nil {
				return InternalError(err)
			}
		} else {
			return InternalError(err)
		}
	}

	if addr == transport.myAddr {
		err := store.Close(false)
		if err != nil {
			shared.LogErrorf("error closing store: %s", err)
		}

		store = nil
		transport = nil
		service = nil
	}

	return EmptySyncResponse
}

func clusterDbQuery(q string) (*rqdb.Rows, error) {
	if store == nil {
		return nil, fmt.Errorf("cluster db not initialized")
	}

	// XXX: Strong here will be Very Slow; we should probably use Weak, but
	// then we need to do some additional handling and redirecting.
	result, err := store.Query([]string{q}, false, true, rqstore.Strong)
	if err != nil {
		return nil, err
	}

	if len(result) != 1 {
		return nil, fmt.Errorf("wrong number of rows, expected %d", len(result))
	}

	return result[0], err
}

func ClusterInfo() ([]shared.ClusterMember, error) {
	if store == nil {
		shared.LogErrorf("no store")
		return nil, nil
	}
	leader := store.Leader()

	nodes, err := store.Nodes()
	if err != nil {
		return nil, err
	}

	foundAll := true
	for _, n := range nodes {
		found := false

		for _, m := range members {
			if n == m.Addr {
				found = true
				break
			}
		}

		if !found {
			foundAll = false
			break
		}
	}

	if foundAll {
		return members, nil
	}

	rows, err := clusterDbQuery("SELECT name, addr, certificate FROM cluster_members")
	if err != nil {
		return nil, err
	}

	members = []shared.ClusterMember{}

	for _, r := range rows.Values {
		m := shared.ClusterMember{}

		n := make([]byte, len(r[0].([]byte)))
		copy(n, r[0].([]byte))
		a := make([]byte, len(r[1].([]byte)))
		copy(a, r[1].([]byte))

		m.Name = string(n)
		m.Addr = string(a)
		m.Certificate = r[2].(string)
		m.Leader = leader == m.Addr

		members = append(members, m)
	}

	if len(members) != len(nodes) {
		return nil, fmt.Errorf("didn't get all members from the db")
	}

	return members, nil
}

func addMemberStmt(addr, name, certificate string) string {
	// XXX: this could be sql injected. unfortunately rqlite
	// doesn't support prepared statements:
	// https://github.com/rqlite/rqlite/issues/140
	return fmt.Sprintf("INSERT INTO cluster_members (addr, name, certificate) values ('%s', '%s', '%s')", addr, name, certificate)
}
