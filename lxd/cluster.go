package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/raft"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"

	rqdb "github.com/rqlite/rqlite/db"
	rqstore "github.com/rqlite/rqlite/store"
)

var (
	transport   *LXDTransport
	peerStore   *LXDPeerStore
	store       *rqstore.Store
	members     []shared.ClusterMember
	shutdown    chan bool
	observation chan raft.Observation

	firstLeaderCert *x509.Certificate
)

func isNotLeaderErr(err error) bool {
	return err == rqstore.ErrNotLeader || err == raft.ErrNotLeader
}

func ClusterMode() bool {
	return transport != nil
}

func MyClusterName() string {
	m, err := peerStore.MemberByAddr(transport.myAddr)
	if err != nil {
		shared.LogErrorf("no cluster name for %s", transport.myAddr)
		return ""
	}

	return m.Name
}

func MyClusterId() (int, error) {
	rows, err := clusterDbQuery(fmt.Sprintf("SELECT id FROM cluster_nodes WHERE addr='%s'", transport.myAddr))
	if err != nil {
		return -1, err
	}

	for _, r := range rows.Values {
		return int(r[0].(float64)), nil
	}

	return -1, fmt.Errorf("my cluster id wasn't found")
}

func GetClusterTarget(clusterTarget string) (*shared.ClusterMember, error) {
	return peerStore.MemberByName(clusterTarget)
}

// LXDTransport basically wraps our websocket API, creating a transport layer
// on top of it for raft to use.
type LXDTransport struct {
	myAddr string
	queue  chan (net.Conn)
	stop   chan (bool)
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
	m, err := peerStore.MemberByAddr(address)
	if err != nil {
		return nil, err
	}

	cert, err := shared.ParseCert(m.Certificate)
	if err != nil {
		return nil, err
	}

	config, err := shared.GetTLSConfig(shared.VarPath("server.crt"), shared.VarPath("server.key"), "", cert)
	if err != nil {
		return nil, err
	}

	dialer := websocket.Dialer{
		TLSClientConfig: config,
		NetDial:         shared.RFC3493Dialer,
	}

	url := fmt.Sprintf("wss://%s/internal/raft/connect", address)
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

	peerStore, err = NewLXDPeerStore()
	if err != nil {
		return err
	}

	store = rqstore.New(&rqstore.StoreConfig{
		DBConf:    config,
		Dir:       shared.VarPath("rqlite"),
		Tn:        transport,
		PeerStore: peerStore,
	})

	err = store.Open(leader)
	if err != nil {
		store.Close(false)
		store = nil
		return err
	}

	observation = make(chan raft.Observation, 1)
	shutdown = make(chan bool, 1)

	store.RegisterObserver(raft.NewObserver(observation, true, nil))
	go observer()
	return nil
}

func StopRQLite() error {
	err := store.Close(false)

	transport = nil
	store = nil
	peerStore = nil

	shutdown <- true
	close(shutdown)

	os.RemoveAll(shared.VarPath("rqlite"))

	return err
}

// /internal/raft/connect
type raftConnResponse struct {
	req *http.Request
}

func (r *raftConnResponse) Render(w http.ResponseWriter) error {
	if transport == nil {
		return fmt.Errorf("clustering not enabled")
	}

	conn, err := shared.WebsocketUpgrader.Upgrade(w, r.req, nil)
	if err != nil {
		return err
	}

	if r.req.TLS == nil {
		return fmt.Errorf("No client certificate provided")
	}

	firstLeaderCert = r.req.TLS.PeerCertificates[len(r.req.TLS.PeerCertificates)-1]
	transport.queue <- &WebsocketToNetConn{conn: conn}
	return nil
}

func (r *raftConnResponse) String() string {
	return "cluster connection"
}

func raftConnectGet(d *Daemon, r *http.Request) Response {
	return &raftConnResponse{r}
}

var raftConnectCmd = Command{name: "raft/connect", get: raftConnectGet}

// /1.0/cluster
func clusterGet(d *Daemon, r *http.Request) Response {
	state := "DISABLED"
	if ClusterMode() {
		state = "OK"
	}

	ret := map[string]interface{}{
		"state": state,
		"keys":  []string{},
	}

	return SyncResponse(true, ret)
}

type clusterPostReq struct {
	Name   string "json:`name`"
	Leader bool   "json:`leader`"
}

func clusterPost(d *Daemon, r *http.Request) Response {
	if transport != nil {
		return BadRequest(fmt.Errorf("clustering already enabled"))
	}

	req := clusterPostReq{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	err = StartRQLite(d, req.Leader)
	if err != nil {
		return InternalError(err)
	}

	if req.Leader {
		if req.Name == "" {
			return BadRequest(fmt.Errorf("must supply a name to the cluster leader"))
		}

		_, err := store.WaitForLeader(5 * time.Second)
		if err != nil {
			StopRQLite()
			return InternalError(err)
		}

		cert, err := ioutil.ReadFile(shared.VarPath("server.crt"))
		if err != nil {
			StopRQLite()
			return InternalError(err)
		}

		addr, err := d.ClusterAddr()
		if err != nil {
			StopRQLite()
			return InternalError(err)
		}

		me := addMemberStmt(addr, req.Name, string(cert))

		result, err := store.Execute([]string{CURRENT_SCHEMA, enableForeignKeys, me}, false, false)
		if err != nil {
			StopRQLite()
			return InternalError(err)
		}

		for _, r := range result {
			if r.Error != "" {
				StopRQLite()
				return InternalError(fmt.Errorf(r.Error))
			}
		}

		err = peerStore.RefreshMembers()
		if err != nil {
			StopRQLite()
			return InternalError(err)
		}
	}

	return EmptySyncResponse
}

var clusterCmd = Command{
	name: "cluster",
	get:  clusterGet,
	post: clusterPost,
}

// /1.0/cluster/nodes
func clusterNodesGet(d *Daemon, r *http.Request) Response {
	if peerStore == nil {
		return BadRequest(fmt.Errorf("clustering not enabled"))
	}
	return SyncResponse(true, shared.ClusterStatus{peerStore.members})
}

var clusterNodesPost = onLeaderHandler{
	leader: func(d *Daemon, r *http.Request, target *shared.ClusterMember) error {
		m := shared.ClusterMember{}

		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			return err
		}

		/* The order here is important: when the next leader election
		 * happens, we need to have this row in the database so that
		 * people can get the new cluster state when the new leader
		 * election happens.
		 */
		_, err = store.Execute([]string{addMemberStmt(m.Addr, m.Name, m.Certificate)}, false, true)
		if err != nil {
			return err
		}

		/* Manually adjust the member list to include this one so we
		 * remember it's certificate and connect to it.
		 */
		err = peerStore.AddPeer(m)
		if err != nil {
			return err
		}

		err = store.Join(m.Addr)
		if err != nil {
			// manually un-adjust the member list
			newMembers := []shared.ClusterMember{}
			for _, om := range members {
				if m.Addr != om.Addr {
					newMembers = append(newMembers, om)
				}
			}

			err = peerStore.SetMembers(newMembers)
			if err != nil {
				shared.LogErrorf("error adjusting to old members")
			}

			// XXX: see note elsewhere about prepared statements
			_, err2 := store.Execute([]string{fmt.Sprintf("DELETE FROM cluster_nodes WHERE name = '%s'", m.Name)}, false, true)
			if err2 != nil {
				return fmt.Errorf("error deleting node from cluster on failed join: %v: %v", err2, err)
			}
			return err
		}

		_, err = store.WaitForLeader(100 * time.Second)
		if err != nil {
			return err
		}

		return nil
	},
}

var clusterNodesCmd = Command{
	name: "cluster/nodes",
	get:  clusterNodesGet,
	post: clusterNodesPost.handle,
}

type bytesReadCloser struct {
	*bytes.Reader
}

func (brc bytesReadCloser) Close() error {
	/* no-op */
	return nil
}

func NewBytesReadCloser(r io.Reader) (bytesReadCloser, error) {
	bodyContent, err := ioutil.ReadAll(r)
	if err != nil {
		return bytesReadCloser{}, err
	}

	return bytesReadCloser{bytes.NewReader(bodyContent)}, nil
}

type onLeaderHandler struct {
	getTarget func(d *Daemon, r *http.Request) (*shared.ClusterMember, error)
	leader    func(d *Daemon, r *http.Request, m *shared.ClusterMember) error
	target    func(d *Daemon, r *http.Request) Response
}

func connectTo(addr string, serverCert string) (*lxd.Client, error) {
	// TODO: maybe cache these somewhere?
	cert, err := ioutil.ReadFile(shared.VarPath("server.crt"))
	if err != nil {
		return nil, err
	}

	key, err := ioutil.ReadFile(shared.VarPath("server.key"))
	if err != nil {
		return nil, err
	}

	return lxd.NewClientFromInfo(lxd.ConnectInfo{
		Name: addr,
		RemoteConfig: lxd.RemoteConfig{
			Addr: addr,
		},
		ClientPEMCert: string(cert),
		ClientPEMKey:  string(key),
		ServerPEMCert: string(serverCert),
	})
}

func forwardRequest(m *shared.ClusterMember, path string, r *http.Request) (*http.Response, *lxd.Client, error) {
	l, err := connectTo(m.Addr, m.Certificate)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequest(r.Method, l.BaseURL+path, r.Body)
	if err != nil {
		return nil, nil, err
	}
	resp, err := l.Http.Do(req)
	return resp, l, err
}

func (olh *onLeaderHandler) handle(d *Daemon, r *http.Request) Response {
	forwardToLeader := r.FormValue("forwardToLeader") == "true"

	bodyContent, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	body := bytesReadCloser{bytes.NewReader(bodyContent)}
	r.Body = body

	var target *shared.ClusterMember
	if olh.getTarget != nil {
		target, err = olh.getTarget(d, r)
		if err != nil {
			return InternalError(err)
		}
	}

	_, err = body.Seek(0, 0)
	if err != nil {
		return InternalError(err)
	}

	/* if this host is not the target, simply forward the request to the
	 * actual target.
	 */
	if !forwardToLeader && target != nil && target.Addr != transport.myAddr {
		resp, _, err := forwardRequest(target, r.URL.Path, r)
		if err != nil {
			return InternalError(err)
		}

		return &rerenderResponse{resp}
	}

	err = olh.leader(d, r, target)
	_, err2 := body.Seek(0, 0)
	if err2 != nil {
		return InternalError(err2)
	}

	if isNotLeaderErr(err) {
		leader, err := peerStore.Leader()
		if err != nil {
			return InternalError(err)
		}

		path := appendQueryParam(r.URL.Path, "forwardToLeader", "true")
		resp, _, err := forwardRequest(leader, path, r)
		if err != nil {
			return InternalError(err)
		}

		if resp.StatusCode >= 300 {
			return &rerenderResponse{resp}
		}

		_, err = body.Seek(0, 0)
		if err != nil {
			return InternalError(err)
		}
	} else if err == raft.ErrRaftShutdown {
		/* no-op: in the delete case, this node's raft can possibly
		 * race and be shutdown before this request is made; let's
		 * ignore this error and just ignore the target code below.
		 */
	} else if err != nil {
		return InternalError(err)
	}

	if forwardToLeader {
		/* Even if the underlying call is async, the cluster forwarded
		 * bit is synchronous, and should give a 200 for the other bits
		 * to continue.
		 */
		return EmptySyncResponse
	}

	if olh.target == nil {
		return EmptySyncResponse
	}

	return olh.target(d, r)
}

func clusterNodeNameGet(d *Daemon, r *http.Request) Response {
	m, err := peerStore.MemberByAddr(transport.myAddr)
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, m)
}

type clusterNodePostRequest struct {
	Name string `json:"name"`
}

var clusterNodeNamePost = onLeaderHandler{
	leader: func(d *Daemon, r *http.Request, m *shared.ClusterMember) error {
		oldName := mux.Vars(r)["name"]

		req := clusterNodePostRequest{}
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return err
		}

		stmt := fmt.Sprintf("UPDATE cluster_nodes SET name='%s' WHERE name='%s'", req.Name, oldName)
		_, err = store.Execute([]string{stmt}, false, true)
		return err
	},
}

var clusterNodeNameDelete = onLeaderHandler{
	getTarget: func(d *Daemon, r *http.Request) (*shared.ClusterMember, error) {
		return peerStore.MemberByName(mux.Vars(r)["name"])
	},
	leader: func(d *Daemon, r *http.Request, m *shared.ClusterMember) error {
		_, err := store.Execute([]string{fmt.Sprintf("DELETE FROM cluster_nodes WHERE name = '%s'", m.Name)}, false, true)
		if err != nil {
			shared.LogErrorf("Failed removing %s from cluster members: %v", m.Name, err)
		}

		return store.Remove(m.Addr)
	},
	target: func(d *Daemon, r *http.Request) Response {
		err := StopRQLite()
		if err != nil {
			return InternalError(err)
		} else {
			return EmptySyncResponse
		}
	},
}

var clusterNodeNameCmd = Command{
	name:   "cluster/nodes/{name}",
	get:    clusterNodeNameGet,
	post:   clusterNodeNamePost.handle,
	delete: clusterNodeNameDelete.handle,
}

func clusterDbQuery(q string) (*rqdb.Rows, error) {
	if store == nil {
		return nil, fmt.Errorf("cluster db not initialized")
	}

	result, err := store.Query([]string{q}, false, true, rqstore.Weak)
	if isNotLeaderErr(err) {
		leader, err := peerStore.Leader()
		if err != nil {
			return nil, err
		}

		l, err := connectTo(leader.Addr, leader.Certificate)
		if err != nil {
			return nil, err
		}

		return l.ClusterDBQuery(q)
	} else if err != nil {
		return nil, err
	}

	if len(result) != 1 {
		return nil, fmt.Errorf("wrong number of rows, expected %d", len(result))
	}

	return result[0], err
}

func clusterDbExecute(q string) error {
	results, err := store.Execute([]string{q}, false, true)
	if isNotLeaderErr(err) {
		leader, err := peerStore.Leader()
		if err != nil {
			return err
		}

		l, err := connectTo(leader.Addr, leader.Certificate)
		if err != nil {
			return err
		}

		_, err = l.ClusterDBExecute(q)
		return err
	}

	for _, r := range results {
		if r.Error != "" {
			return fmt.Errorf(r.Error)
		}
	}

	return err
}

func ClusterMembers() []shared.ClusterMember {
	if peerStore == nil {
		return nil
	}
	return peerStore.members
}

func addMemberStmt(addr, name, certificate string) string {
	// XXX: this could be sql injected. unfortunately rqlite
	// doesn't support prepared statements:
	// https://github.com/rqlite/rqlite/issues/140
	return fmt.Sprintf("INSERT INTO cluster_nodes (addr, name, certificate) values ('%s', '%s', '%s')", addr, name, certificate)
}

func observer() {
	for {
		select {
		case <-shutdown:
			shutdown = nil
			observation = nil
			return
		case o := <-observation:
			switch d := o.Data.(type) {
			case raft.RaftState:
				switch d {
				case raft.Shutdown:
					StopRQLite()
				case raft.Candidate, raft.Follower, raft.Leader:
					/* in principle we don't need to do
					 * anything here, because we'll see new
					 * leader observations below
					 */
					break
				default:
					shared.LogErrorf("unknown raft state: %#v", o)
				}
			case raft.LeaderObservation:
				/* The raft instance can actually be dead and
				 * fire an event that says there is no leader
				 * because it starts a re-election once a node
				 * leaves. We may receive the shutdown event
				 * first, but we may not. In any case, let's
				 * ignore this.
				 */
				if store == nil {
					break
				}

				/* a new leader was elected, let's refresh our
				 * list of cluster members.
				 */
				err := peerStore.RefreshMembers()
				if err != nil {
					shared.LogErrorf("error refreshing cluster members: %v", err)
				}
			case raft.RequestVoteRequest:
				/* we don't care about voting */
				break
			default:
				shared.LogErrorf("unknown observation from raft %#v", o)
			}
		}
	}
}

func appendQueryParam(oldPath string, key string, value string) string {
	path := oldPath
	q := url.Values{key: []string{value}}.Encode()
	if strings.Contains(path, "?") {
		path += "&" + q
	} else {
		path += "?" + q
	}

	return path
}

func clusterDBGet(d *Daemon, r *http.Request) Response {
	q := r.FormValue("q")

	/* do a specific query if asked */
	if q != "" {
		result, err := store.Query([]string{q}, false, true, rqstore.Weak)
		if err != nil {
			return InternalError(err)
		}

		if len(result) != 1 {
			return InternalError(fmt.Errorf("wrong number of results, got %d", len(result)))
		}

		if result[0].Error != "" {
			return InternalError(fmt.Errorf(result[0].Error))
		}

		return SyncResponse(true, result[0])
	}

	data, err := store.Database(false)
	if err != nil {
		return InternalError(err)
	}

	files := []fileResponseEntry{fileResponseEntry{
		identifier: "dump.sql",
		filename:   "dump.sql",
		buffer:     data,
	}}

	return FileResponse(r, files, nil, false)
}

func clusterDBPost(d *Daemon, r *http.Request) Response {
	q := ""

	err := json.NewDecoder(r.Body).Decode(&q)
	if err != nil {
		return BadRequest(err)
	}

	results, err := store.Execute([]string{q}, false, true)
	if err != nil {
		return InternalError(err)
	}

	if len(results) != 1 {
		return InternalError(fmt.Errorf("unexpected number of results %d", len(results)))
	}

	return SyncResponse(true, results[0])
}

/*
var clusterDBPost = onLeaderHandler{
	leader: func(d *Daemon, r *http.Request, m *shared.ClusterMember) error {
		qs := []string{}

		err := json.NewDecoder(r.Body).Decode(&qs)
		if err != nil {
			return err
		}

		for _, q := range qs {
			if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(q)), "select") {
				return fmt.Errorf("select statements are not allowed via this interface")
			}
		}

		results, err := store.Execute(qs, false, true)
		if err != nil {
			return err
		}

		for _, r := range results {
			if r.Error != "" {
				return fmt.Errorf(r.Error)
			}
		}

		return nil
	},
}
*/

var clusterDBCmd = Command{
	name: "cluster/db",
	get:  clusterDBGet,
	post: clusterDBPost,
}
