package main

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/pborman/uuid"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var operationsLock sync.Mutex
var operations map[string]*operation = make(map[string]*operation)

type operationClass int

const (
	operationClassTask      operationClass = 1
	operationClassWebsocket operationClass = 2
	operationClassToken     operationClass = 3
	operationClassProxy     operationClass = 4
)

func (t operationClass) String() string {
	return map[operationClass]string{
		operationClassTask:      "task",
		operationClassWebsocket: "websocket",
		operationClassToken:     "token",
		operationClassProxy:     "proxy",
	}[t]
}

type operation struct {
	id        string
	class     operationClass
	createdAt time.Time
	updatedAt time.Time
	status    api.StatusCode
	url       string
	resources map[string][]string
	metadata  map[string]interface{}
	err       string
	readonly  bool

	// Those functions are called at various points in the operation lifecycle
	onRun     func(*operation) error
	onCancel  func(*operation) error
	onConnect func(*operation, *http.Request, http.ResponseWriter) error

	// Channels used for error reporting and state tracking of background actions
	chanDone chan error

	// Locking for concurent access to the operation
	lock sync.Mutex
}

func (op *operation) done() {
	if op.readonly {
		return
	}

	op.lock.Lock()
	op.readonly = true
	op.onRun = nil
	op.onCancel = nil
	op.onConnect = nil
	close(op.chanDone)
	op.lock.Unlock()

	time.AfterFunc(time.Second*5, func() {
		operationsLock.Lock()
		_, ok := operations[op.id]
		if !ok {
			operationsLock.Unlock()
			return
		}

		delete(operations, op.id)
		operationsLock.Unlock()

		if ClusterMode() {
			err := clusterDbExecute([]string{fmt.Sprintf("DELETE FROM operations WHERE uuid='%s'", op.id)})
			if err != nil {
				shared.LogErrorf("failed to remove operation %s from cluster db: %s", op.id, err)
			}
		}

		/*
		 * When we create a new lxc.Container, it adds a finalizer (via
		 * SetFinalizer) that frees the struct. However, it sometimes
		 * takes the go GC a while to actually free the struct,
		 * presumably since it is a small amount of memory.
		 * Unfortunately, the struct also keeps the log fd open, so if
		 * we leave too many of these around, we end up running out of
		 * fds. So, let's explicitly do a GC to collect these at the
		 * end of each request.
		 */
		runtime.GC()
	})
}

func (op *operation) Run() (chan error, error) {
	if op.status != api.Pending {
		return nil, fmt.Errorf("Only pending operations can be started")
	}

	chanRun := make(chan error, 1)

	op.lock.Lock()
	op.status = api.Running

	if op.onRun != nil {
		go func(op *operation, chanRun chan error) {
			err := op.onRun(op)
			if err != nil {
				op.lock.Lock()
				op.status = api.Failure
				op.err = SmartError(err).String()
				op.lock.Unlock()
				op.done()
				chanRun <- err

				shared.LogDebugf("Failure for %s operation: %s: %s", op.class.String(), op.id, err)

				_, md, _ := op.Render()
				eventSend("operation", md)
				return
			}

			op.lock.Lock()
			op.status = api.Success
			op.lock.Unlock()
			op.done()
			chanRun <- nil

			op.lock.Lock()
			shared.LogDebugf("Success for %s operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			eventSend("operation", md)
			op.lock.Unlock()
		}(op, chanRun)
	}
	op.lock.Unlock()

	shared.LogDebugf("Started %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	return chanRun, nil
}

func (op *operation) Cancel() (chan error, error) {
	if op.status != api.Running {
		return nil, fmt.Errorf("Only running operations can be cancelled")
	}

	if !op.mayCancel() {
		return nil, fmt.Errorf("This operation can't be cancelled")
	}

	chanCancel := make(chan error, 1)

	op.lock.Lock()
	oldStatus := op.status
	op.status = api.Cancelling
	op.lock.Unlock()

	if op.onCancel != nil {
		go func(op *operation, oldStatus api.StatusCode, chanCancel chan error) {
			err := op.onCancel(op)
			if err != nil {
				op.lock.Lock()
				op.status = oldStatus
				op.lock.Unlock()
				chanCancel <- err

				shared.LogDebugf("Failed to cancel %s operation: %s: %s", op.class.String(), op.id, err)
				_, md, _ := op.Render()
				eventSend("operation", md)
				return
			}

			op.lock.Lock()
			op.status = api.Cancelled
			op.lock.Unlock()
			op.done()
			chanCancel <- nil

			shared.LogDebugf("Cancelled %s operation: %s", op.class.String(), op.id)
			_, md, _ := op.Render()
			eventSend("operation", md)
		}(op, oldStatus, chanCancel)
	}

	shared.LogDebugf("Cancelling %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	if op.onCancel == nil {
		op.lock.Lock()
		op.status = api.Cancelled
		op.lock.Unlock()
		op.done()
		chanCancel <- nil
	}

	shared.LogDebugf("Cancelled %s operation: %s", op.class.String(), op.id)
	_, md, _ = op.Render()
	eventSend("operation", md)

	return chanCancel, nil
}

func (op *operation) Connect(r *http.Request, w http.ResponseWriter) (chan error, error) {
	if op.class != operationClassWebsocket {
		return nil, fmt.Errorf("Only websocket operations can be connected")
	}

	if op.status != api.Running {
		return nil, fmt.Errorf("Only running operations can be connected")
	}

	chanConnect := make(chan error, 1)

	op.lock.Lock()

	go func(op *operation, chanConnect chan error) {
		err := op.onConnect(op, r, w)
		if err != nil {
			chanConnect <- err

			shared.LogDebugf("Failed to handle %s operation: %s: %s", op.class.String(), op.id, err)
			return
		}

		chanConnect <- nil

		shared.LogDebugf("Handled %s operation: %s", op.class.String(), op.id)
	}(op, chanConnect)
	op.lock.Unlock()

	shared.LogDebugf("Connected %s operation: %s", op.class.String(), op.id)

	return chanConnect, nil
}

func (op *operation) mayCancel() bool {
	return op.onCancel != nil || op.class == operationClassToken
}

func (op *operation) Render() (string, *api.Operation, error) {
	// Setup the resource URLs
	resources := op.resources
	if resources != nil {
		tmpResources := make(map[string][]string)
		for key, value := range resources {
			var values []string
			for _, c := range value {
				values = append(values, fmt.Sprintf("/%s/%s/%s", version.APIVersion, key, c))
			}
			tmpResources[key] = values
		}
		resources = tmpResources
	}

	return op.url, &api.Operation{
		ID:         op.id,
		Class:      op.class.String(),
		CreatedAt:  op.createdAt,
		UpdatedAt:  op.updatedAt,
		Status:     op.status.String(),
		StatusCode: op.status,
		Resources:  resources,
		Metadata:   op.metadata,
		MayCancel:  op.mayCancel(),
		Err:        op.err,
	}, nil
}

func (op *operation) WaitFinal(timeout int) (bool, error) {
	// Check current state
	if op.status.IsFinal() {
		return true, nil
	}

	// Wait indefinitely
	if timeout == -1 {
		for {
			<-op.chanDone
			return true, nil
		}
	}

	// Wait until timeout
	if timeout > 0 {
		timer := time.NewTimer(time.Duration(timeout) * time.Second)
		for {
			select {
			case <-op.chanDone:
				return false, nil

			case <-timer.C:
				return false, nil
			}
		}
	}

	return false, nil
}

func (op *operation) UpdateResources(opResources map[string][]string) error {
	if op.status != api.Pending && op.status != api.Running {
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		return fmt.Errorf("Read-only operations can't be updated")
	}

	op.lock.Lock()
	op.updatedAt = time.Now()
	op.resources = opResources
	op.lock.Unlock()

	shared.LogDebugf("Updated resources for %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	return nil
}

func (op *operation) UpdateMetadata(opMetadata interface{}) error {
	if op.status != api.Pending && op.status != api.Running {
		return fmt.Errorf("Only pending or running operations can be updated")
	}

	if op.readonly {
		return fmt.Errorf("Read-only operations can't be updated")
	}

	newMetadata, err := shared.ParseMetadata(opMetadata)
	if err != nil {
		return err
	}

	op.lock.Lock()
	op.updatedAt = time.Now()
	op.metadata = newMetadata
	op.lock.Unlock()

	shared.LogDebugf("Updated metadata for %s operation: %s", op.class.String(), op.id)
	_, md, _ := op.Render()
	eventSend("operation", md)

	return nil
}

func operationCreate(opClass operationClass, opResources map[string][]string, opMetadata interface{},
	onRun func(*operation) error,
	onCancel func(*operation) error,
	onConnect func(*operation, *http.Request, http.ResponseWriter) error) (*operation, error) {

	// Main attributes
	op := operation{}
	op.id = uuid.NewRandom().String()
	op.class = opClass
	op.createdAt = time.Now()
	op.updatedAt = op.createdAt
	op.status = api.Pending
	op.url = fmt.Sprintf("/%s/operations/%s", version.APIVersion, op.id)
	op.resources = opResources
	op.chanDone = make(chan error)

	newMetadata, err := shared.ParseMetadata(opMetadata)
	if err != nil {
		return nil, err
	}
	op.metadata = newMetadata

	// Callback functions
	op.onRun = onRun
	op.onCancel = onCancel
	op.onConnect = onConnect

	// Sanity check
	if op.class != operationClassWebsocket && op.onConnect != nil {
		return nil, fmt.Errorf("Only websocket operations can have a Connect hook")
	}

	if op.class == operationClassWebsocket && op.onConnect == nil {
		return nil, fmt.Errorf("Websocket operations must have a Connect hook")
	}

	if op.class == operationClassToken && op.onRun != nil {
		return nil, fmt.Errorf("Token operations can't have a Run hook")
	}

	if op.class == operationClassToken && op.onCancel != nil {
		return nil, fmt.Errorf("Token operations can't have a Cancel hook")
	}

	operationsLock.Lock()
	operations[op.id] = &op
	operationsLock.Unlock()

	if ClusterMode() {
		id, err := MyClusterId()
		if err != nil {
			return nil, err
		}

		err = clusterDbExecute([]string{fmt.Sprintf("INSERT INTO operations (uuid, cluster_id) VALUES ('%s', '%d')", op.id, id)})
		if err != nil {
			return nil, err
		}
	}

	_, md, _ := op.Render()
	eventSend("operation", md)

	return &op, nil
}

func operationGet(id string) (*operation, error) {
	operationsLock.Lock()
	op, ok := operations[id]
	operationsLock.Unlock()

	if !ok {
		return nil, fmt.Errorf("Operation '%s' doesn't exist", id)
	}

	return op, nil
}

// API functions
func operationFindOrForward(r *http.Request) (*operation, Response) {
	id := mux.Vars(r)["id"]

	op, err := operationGet(id)
	if err != nil {
		if !ClusterMode() {
			return nil, NotFound
		}

		q := fmt.Sprintf("SELECT addr FROM cluster_nodes JOIN operations ON operations.cluster_id=cluster_nodes.id WHERE uuid='%s'", id)
		rows, err := clusterDbQuery(q)
		if err != nil {
			return nil, InternalError(err)
		}

		addr := ""
		for _, r := range rows.Values {
			addr = r[0].(string)
			break
		}

		if addr == "" {
			return nil, NotFound
		}

		m, err := peerStore.MemberByAddr(addr)
		if err != nil {
			return nil, InternalError(err)
		}

		/* XXX: need to forward query parms, maybe request contents too */
		resp, _, err := forwardRequest(m, r.URL.Path, r)
		if err != nil {
			return nil, InternalError(err)
		}

		return nil, &rerenderResponse{resp}
	}

	return op, nil
}

func operationAPIGet(d *Daemon, r *http.Request) Response {
	op, resp := operationFindOrForward(r)
	if op == nil {
		return resp
	}

	_, body, err := op.Render()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, body)
}

func operationAPIDelete(d *Daemon, r *http.Request) Response {
	op, resp := operationFindOrForward(r)
	if op == nil {
		return resp
	}

	_, err := op.Cancel()
	if err != nil {
		return BadRequest(err)
	}

	return EmptySyncResponse
}

var operationCmd = Command{name: "operations/{id}", get: operationAPIGet, delete: operationAPIDelete}

func operationsAPIGet(d *Daemon, r *http.Request) Response {
	var md shared.Jmap

	recursion := d.isRecursionRequest(r)

	md = shared.Jmap{}

	operationsLock.Lock()
	ops := operations
	operationsLock.Unlock()

	for _, v := range ops {
		status := strings.ToLower(v.status.String())
		_, ok := md[status]
		if !ok {
			if recursion {
				md[status] = make([]*api.Operation, 0)
			} else {
				md[status] = make([]string, 0)
			}
		}

		if !recursion {
			md[status] = append(md[status].([]string), v.url)
			continue
		}

		_, body, err := v.Render()
		if err != nil {
			continue
		}

		md[status] = append(md[status].([]*api.Operation), body)
	}

	return SyncResponse(true, md)
}

var operationsCmd = Command{name: "operations", get: operationsAPIGet}

func operationAPIWaitGet(d *Daemon, r *http.Request) Response {
	op, resp := operationFindOrForward(r)
	if op == nil {
		return resp
	}

	timeout, err := shared.AtoiEmptyDefault(r.FormValue("timeout"), -1)
	if err != nil {
		return InternalError(err)
	}

	_, err = op.WaitFinal(timeout)
	if err != nil {
		return InternalError(err)
	}

	_, body, err := op.Render()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, body)
}

var operationWait = Command{name: "operations/{id}/wait", get: operationAPIWaitGet}

type operationWebSocket struct {
	req *http.Request
	op  *operation
}

func (r *operationWebSocket) Render(w http.ResponseWriter) error {
	chanErr, err := r.op.Connect(r.req, w)
	if err != nil {
		return err
	}

	err = <-chanErr
	return err
}

func (r *operationWebSocket) String() string {
	_, md, err := r.op.Render()
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}

	return md.ID
}

func operationAPIWebsocketGet(d *Daemon, r *http.Request) Response {
	/* XXX: does this really work? */
	op, resp := operationFindOrForward(r)
	if op == nil {
		return resp
	}

	return &operationWebSocket{r, op}
}

var operationWebsocket = Command{name: "operations/{id}/websocket", untrustedGet: true, get: operationAPIWebsocketGet}
