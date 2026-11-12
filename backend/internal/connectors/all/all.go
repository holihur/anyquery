// Package all wires every built-in connector into the registry. Add a new
// connector by importing it here (or by calling Register from your own init).
package all

import (
	_ "github.com/anyquery/anyquery/internal/accessdb"
	_ "github.com/anyquery/anyquery/internal/bigquery"
	_ "github.com/anyquery/anyquery/internal/cassandra"
	_ "github.com/anyquery/anyquery/internal/cloudsql"
	_ "github.com/anyquery/anyquery/internal/druid"
	_ "github.com/anyquery/anyquery/internal/elasticsearch"
	_ "github.com/anyquery/anyquery/internal/excel"
	_ "github.com/anyquery/anyquery/internal/httpjson"
	_ "github.com/anyquery/anyquery/internal/influxdb"
	_ "github.com/anyquery/anyquery/internal/mongodb"
	"github.com/anyquery/anyquery/internal/presets"
	_ "github.com/anyquery/anyquery/internal/redisdb"
	_ "github.com/anyquery/anyquery/internal/sls"
	"github.com/anyquery/anyquery/internal/sqlconn"
	_ "github.com/anyquery/anyquery/internal/tdengine"
)

func init() {
	sqlconn.RegisterAll()
	presets.RegisterAll()
}
