package response

import (
	"encoding/json"
	"net/http"
)

// DecodeJSON decodes the request body into dst.
// DisallowUnknownFields rejects requests with fields not in the struct —
// prevents silent data loss from typos and undocumented fields.
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
