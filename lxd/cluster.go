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
)

var (
	transport *LXDTransport
	store     *rqstore.Store
	service   *rqhttp.Service
)

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

func StartRQLite(d *Daemon, myAddr string, leader bool) error {
	config := rqstore.NewDBConfig("", true)
	transport = &LXDTransport{
		myAddr: myAddr,
		queue:  make(chan net.Conn, 10),
		stop:   make(chan bool),
	}

	store = rqstore.New(config, shared.VarPath("rqlite"), transport)

	err := store.Open(leader)
	if err != nil {
		store.Close(false)
		store = nil
		return err
	}

	err = daemonConfig["cluster.raft_address"].Set(d, myAddr)
	if err != nil {
		transport = nil
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
	nodes, err := store.Nodes()
	if err != nil {
		return InternalError(err)
	}

	leader := store.Leader()
	if err != nil {
		return InternalError(err)
	}

	ret := []shared.ClusterMember{}

	for _, n := range nodes {
		ret = append(ret, shared.ClusterMember{Leader: leader == n, Addr: n})
	}

	return SyncResponse(true, shared.ClusterStatus{ret})
}

func clusterPost(d *Daemon, r *http.Request) Response {
	leader := r.FormValue("leader")
	addr := r.FormValue("addr")

	if addr == "" {
		return BadRequest(fmt.Errorf("must provide address for raft"))
	}

	err := StartRQLite(d, addr, leader == "true")
	if err != nil {
		return InternalError(err)
	}

	if leader == "true" {
		_, err := store.WaitForLeader(5 * time.Second)
		if err != nil {
			store = nil
			return InternalError(err)
		}
	}

	return EmptySyncResponse
}

func clusterPatch(d *Daemon, r *http.Request) Response {
	req := []string{}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	for _, addr := range req {
		// XXX: need to guarantee that this is the leader or redirect
		err := store.Join(addr)
		if err != nil {
			return InternalError(err)
		}
	}

	return EmptySyncResponse
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

var clusterCmd = Command{
	name: "cluster",
	get: clusterGet,
	post: clusterPost,
	patch: clusterPatch,
	delete: clusterDelete,
}
