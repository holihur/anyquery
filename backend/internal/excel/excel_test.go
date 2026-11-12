package excel

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/anyquery/anyquery/internal/connector"

	"github.com/xuri/excelize/v2"
)

func TestExcelIntrospectAndFetch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.xlsx")
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Orders"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		t.Fatal(err)
	}
	f.SetActiveSheet(idx)
	rows := [][]any{
		{"id", "customer", "amount", "paid"},
		{1, "Acme", 120.5, true},
		{2, "Globex", 80, false},
	}
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	c, err := (Provider{}).Open(context.Background(), map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tables, err := c.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) == 0 || tables[0].Name != "Orders" {
		t.Fatalf("tables = %+v", tables)
	}
	res, err := c.Fetch(context.Background(), connectorFetch("Orders", 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0]["customer"] != "Acme" || res.Rows[0]["amount"] != 120.5 {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func connectorFetch(table string, limit int) connector.FetchRequest {
	return connector.FetchRequest{Table: table, Limit: limit}
}
