package main

import (
	"encoding/json"
	"net/http"
)

type Jmap map[string]interface{}

func SyncResponse(success bool, metadata Jmap, w http.ResponseWriter) {
	result := "success"
	if !success {
		result = "failure"
	}

	err := json.NewEncoder(w).Encode(Jmap{"type": "sync", "result": result, "metadata": metadata})

	if err != nil {
		ErrorResponse(500, "Error encoding sync response", w)
	}
}

func AsyncResponse(id string, w http.ResponseWriter) {
	err := json.NewEncoder(w).Encode(Jmap{"type": "async", "operation": id})
	if err != nil {
		ErrorResponse(500, "Error encoding async response", w)
	}
}

func ErrorResponse(code int, msg string, w http.ResponseWriter) {
	err := json.NewEncoder(w).Encode(Jmap{"type": "error", "code": code, "metadata": msg})

	if err != nil {
		http.Error(w, "Error encoding error response!", 500)
	}

	/* golang says that the error response should just be a string, but
	 * our spec says it could be a json object. We should figure out what
	 * we want to do about that. This foo is a placeholder, since I'm not
	 * exactly sure what will happen with the code above.
	 */
	http.Error(w, "foo", code)
}

/* Some standard responses */
func NotImplemented(w http.ResponseWriter) {
	ErrorResponse(501, "not implemented", w)
}

func NotFound(w http.ResponseWriter) {
	ErrorResponse(404, "not found", w)
}
