package qyserver

import (
	"compress/gzip"
	"context"
	"database/sql"
	"log"
	"os"

	"qy/qy/platform/gq"
)

// runImportGQ turns gotquestions' pg_dump into the SQLite file QY_GQ_DB names:
//
//	qy-server import-gq <dump.sql.gz> <gq.db>
func runImportGQ(args []string) {
	if len(args) != 2 {
		log.Fatal("usage: qy-server import-gq <dump.sql.gz> <gq.db>")
	}
	in, err := os.Open(args[0])
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()
	dump, err := gzip.NewReader(in)
	if err != nil {
		log.Fatal(err)
	}
	tmp := args[1] + ".tmp"
	if _, err := os.Stat(tmp); err == nil {
		log.Fatalf("%s is left from an earlier run; remove it first", tmp)
	}
	db, err := sql.Open("sqlite", "file:"+tmp+"?_pragma=journal_mode(off)&_pragma=synchronous(off)")
	if err != nil {
		log.Fatal(err)
	}
	n, err := gq.Import(context.Background(), db, dump)
	if err == nil {
		err = db.Close()
	}
	if err == nil {
		err = os.Rename(tmp, args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("imported %d packs, %d questions, %d people, %d credits into %s", n.Packs, n.Questions, n.People, n.Authors, args[1])
}
