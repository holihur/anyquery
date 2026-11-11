package api

import (
	"net/http"
	"strconv"
)

func parseLimit(r *http.Request, def int64) int64 {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
