// Package all wires every built-in connector into the registry. Add a new
// connector by importing it here (or by calling Register from your own init).
package all

import (
	_ "github.com/anyquery/anyquery/internal/cloudsql"
	_ "github.com/anyquery/anyquery/internal/elasticsearch"
	_ "github.com/anyquery/anyquery/internal/httpjson"
	_ "github.com/anyquery/anyquery/internal/mongodb"
	_ "github.com/anyquery/anyquery/internal/redisdb"
	"github.com/anyquery/anyquery/internal/sqlconn"
)

func init() {
	sqlconn.RegisterAll()
}
