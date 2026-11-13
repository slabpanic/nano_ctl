package store

import (
	"path/filepath"
	"testing"
)

func TestAliasUpdatePreservesFalseFlagsAndProvenance(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row := IOAlias{CardNo: 0, Kind: "input-bit", Address: 8, Alias: "输入按钮", SourceID: "source-123"}
	if err := db.PutIOAlias(&row); err != nil {
		t.Fatal(err)
	}
	source := row.SourceID
	row.Disabled, row.Reverse = true, true
	if err := db.PutIOAlias(&row); err != nil {
		t.Fatal(err)
	}
	update := IOAlias{Kind: row.Kind, Address: 8, Alias: "新别名"}
	if err := db.PutIOAlias(&update); err != nil {
		t.Fatal(err)
	}
	row, err = db.GetIOAlias(0, row.Kind, 8)
	if err != nil || row.Reverse || row.Disabled || row.SourceID != source {
		t.Fatalf("update: %+v, %v", row, err)
	}
	bad := IOAlias{Kind: "output-bit", Address: 65536, Alias: "非法"}
	if err := db.PutIOAlias(&bad); err == nil {
		t.Fatal("accepted overflowing address")
	}
	bad.Address = 1
	bad.Kind = "analog-output"
	bad.Reverse = true
	if err := db.PutIOAlias(&bad); err == nil {
		t.Fatal("accepted analog reverse")
	}
}
