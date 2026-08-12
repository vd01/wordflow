package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
	_ "modernc.org/sqlite"
)

func q(db *sql.DB, w string) {
	row := db.QueryRow("SELECT word, definition FROM ecdict WHERE word = ?", w)
	var a, b string
	row.Scan(&a, &b)
}

func fuzzy(db *sql.DB, w string) {
	firstChar := strings.ToLower(string(w[0]))
	rows, err := db.Query("SELECT word FROM ecdict WHERE word >= ? AND word < ? LIMIT 500", firstChar, string(firstChar[0]+1))
	if err != nil { return }
	defer rows.Close()
	for rows.Next() { var s string; rows.Scan(&s) }
}

func main() {
	dbPath := os.Args[1]
	db, _ := sql.Open("sqlite", dbPath)
	db.SetMaxOpenConns(1)
	for _, p := range []string{"PRAGMA journal_mode=OFF", "PRAGMA cache_size=-65536", "PRAGMA mmap_size=134217728"} {
		db.Exec(p)
	}
	words := []string{"hello", "abandon", "recieve", "zzzzzzzzzz", "qxjklwprtz", "xy", "miscellaneous", "the", "a", "word"}
	for _, w := range words {
		t := time.Now()
		q(db, w)
		fmt.Printf("cold first-query %-14q exact: %v\n", w, time.Since(t))
		t = time.Now()
		fuzzy(db, w)
		fmt.Printf("cold first-query %-14q fuzzy: %v\n", w, time.Since(t))
	}
}
