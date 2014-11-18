package main

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/lxc/lxd"
)

func SyncResponse(success bool, metadata lxd.Jmap, w http.ResponseWriter) {
	result := "success"
	if !success {
		result = "failure"
	}

	err := json.NewEncoder(w).Encode(lxd.Jmap{"type": lxd.Sync, "result": result, "metadata": metadata})

	if err != nil {
		ErrorResponse(500, "Error encoding sync response", w)
	}
}

func AsyncResponse(id string, w http.ResponseWriter) {
	err := json.NewEncoder(w).Encode(lxd.Jmap{"type": lxd.Async, "operation": id})
	if err != nil {
		ErrorResponse(500, "Error encoding async response", w)
	}
}

func ErrorResponse(code int, msg string, w http.ResponseWriter) {
	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(lxd.Jmap{"type": lxd.Error, "error": msg, "error_code": code})

	if err != nil {
		http.Error(w, "Error encoding error response!", 500)
		return
	}

	http.Error(w, buf.String(), code)
}

/* Some standard responses */
func NotImplemented(w http.ResponseWriter) {
	ErrorResponse(501, "not implemented", w)
}

func NotFound(w http.ResponseWriter) {
	ErrorResponse(404, "not found", w)
}

func Forbidden(w http.ResponseWriter) {
	ErrorResponse(403, "not authorized", w)
}

func BadRequest(w http.ResponseWriter, err error) {
	ErrorResponse(400, err.Error(), w)
}
