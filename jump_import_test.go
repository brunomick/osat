package main

import (
	"testing"

	"osat/internal/store"
)

func TestExportImportKeepsJump(t *testing.T) {
	src := newExportApp(t)
	mustSave(t, src, store.Connection{Name: "bastion", Host: "b", User: "u", AuthType: store.AuthAgent}, "")
	mustSave(t, src, store.Connection{Name: "db", Host: "d", User: "u", AuthType: store.AuthAgent, JumpID: byName(src)["bastion"].ID}, "")
	mustSave(t, src, store.Connection{Name: "cache", Host: "c", User: "u", AuthType: store.AuthAgent, JumpID: byName(src)["bastion"].ID}, "")

	// Las dos juntas: el salto apunta al bastión recién importado.
	dst := newExportApp(t)
	r, err := dst.ImportOsatScan(exportFile(t, src, idsOf(src, "bastion", "db"), "pw", false), "pw")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := dst.ImportOsat(r.Items); err != nil || n != 2 {
		t.Fatalf("ImportOsat = %d, %v", n, err)
	}
	got := byName(dst)
	if got["db"].JumpID != got["bastion"].ID {
		t.Fatalf("db = %+v", got["db"])
	}
	// Solo la que salta: se enlaza con el bastión que ya estaba en este osat.
	r, err = dst.ImportOsatScan(exportFile(t, src, idsOf(src, "cache"), "pw", false), "pw")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := dst.ImportOsat(r.Items); err != nil || n != 1 {
		t.Fatalf("ImportOsat = %d, %v", n, err)
	}
	if got := byName(dst); got["cache"].JumpID != got["bastion"].ID {
		t.Fatalf("cache = %+v", got["cache"])
	}
	// Sin el bastión en ningún lado: se importa sin salto y avisa.
	other := newExportApp(t)
	r, err = other.ImportOsatScan(exportFile(t, src, idsOf(src, "db"), "pw", false), "pw")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := other.ImportOsat(r.Items); err == nil || n != 1 || byName(other)["db"].JumpID != "" {
		t.Fatalf("ImportOsat = %d, %v (db %+v)", n, err, byName(other)["db"])
	}
}
