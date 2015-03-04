package shared

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type OperationStatus int

const (
	OK         OperationStatus = 100
	Started    OperationStatus = 101
	Stopped    OperationStatus = 102
	Running    OperationStatus = 103
	Cancelling OperationStatus = 104
	Pending    OperationStatus = 105

	Success OperationStatus = 200

	Failure   OperationStatus = 400
	Cancelled OperationStatus = 401
)

func (o OperationStatus) String() string {
	return map[OperationStatus]string{
		OK:         "OK",
		Started:    "Started",
		Stopped:    "Stopped",
		Running:    "Running",
		Cancelling: "Cancelling",
		Pending:    "Pending",
		Success:    "Success",
		Failure:    "Failure",
		Cancelled:  "Cancelled",
	}[o]
}

func (o OperationStatus) IsFinal() bool {
	return int(o) >= 200
}

var WebsocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// OperationWebsocket represents the /websocket endpoint for operations. Users
// can connect by specifying a secret (given to them at operation creation
// time). After the first connection to the socket is initiated, the socket's
// Do() function is called. It is up to the Do() function to block and wait
// for any other connections it expects before proceeding.
type OperationWebsocket interface {

	// Metadata() specifies the metadata for the initial response this
	// OperationWebsocket renders.
	Metadata() interface{}

	// Connect should return the error if the connection failed,
	// or nil if the connection was successful.
	Connect(secret string, r *http.Request, w http.ResponseWriter) error

	// Run the actual operation and return its result.
	Do() OperationResult
}

type OperationResult struct {
	Metadata json.RawMessage
	Error    error
}

var OperationSuccess OperationResult = OperationResult{}

func OperationWrap(f func() error) func() OperationResult {
	return func() OperationResult { return OperationError(f()) }
}

func OperationError(err error) OperationResult {
	return OperationResult{nil, err}
}

type Operation struct {
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Status      string          `json:"status"`
	StatusCode  OperationStatus `json:"status_code"`
	ResourceURL string          `json:"resource_url"`
	Metadata    json.RawMessage `json:"metadata"`
	MayCancel   bool            `json:"may_cancel"`

	/* The fields below are for use on the server side. */
	Run func() OperationResult `json:"-"`

	/* If this is not nil, the operation can be cancelled by calling this
	 * function */
	Cancel func() error `json:"-"`

	/* This channel receives exactly one value, when the event is done and
	 * the status is updated */
	Chan chan bool `json:"-"`

	/* If this is not nil, users can connect to a websocket for this
	 * operation. The flag indicates whether or not this socket has already
	 * been used: websockets can be connected to exactly once. */
	Websocket OperationWebsocket `json:"-"`
}

func (o *Operation) GetError() error {
	if o.StatusCode == Failure {
		var s string
		if err := json.Unmarshal(o.Metadata, &s); err != nil {
			return err
		}

		return fmt.Errorf(s)
	}
	return nil
}

func (o *Operation) MetadataAsMap() (*Jmap, error) {
	ret := Jmap{}
	if err := json.Unmarshal(o.Metadata, &ret); err != nil {
		return nil, err
	}
	return &ret, nil
}

func (o *Operation) SetStatus(status OperationStatus) {
	o.Status = status.String()
	o.StatusCode = status
	o.UpdatedAt = time.Now()
	if status.IsFinal() {
		o.MayCancel = false
	}
}

func (o *Operation) SetResult(result OperationResult) {
	o.SetStatusByErr(result.Error)
	if result.Metadata != nil {
		o.Metadata = result.Metadata
	}
	o.Chan <- true
}

func (o *Operation) SetStatusByErr(err error) {
	if err == nil {
		o.SetStatus(Success)
	} else {
		o.SetStatus(Failure)
		md, err := json.Marshal(err.Error())

		/* This isn't really fatal, it'll just be annoying for users */
		if err != nil {
			Debugf("error converting %s to json", err)
		}
		o.Metadata = md
	}
}

func OperationsURL(id string) string {
	return fmt.Sprintf("/%s/operations/%s", APIVersion, id)
}
