package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type EcdictEntry struct {
	Word        string `json:"word"`
	Phonetic    string `json:"phonetic"`
	Definition  string `json:"definition"`
	Translation string `json:"translation"`
	Pos         string `json:"pos"`
	Collins     *int   `json:"collins"`
	Oxford      *int   `json:"oxford"`
	CorrectedFrom string `json:"corrected_from"`
	Tag         string `json:"tag"`
	Bnc         *int   `json:"bnc"`
	Frq         *int   `json:"frq"`
	Exchange    string `json:"exchange"`
}

var db *sql.DB

func openDB(dbPath string) {
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	d.SetMaxOpenConns(1)
	pragmas := []string{
		"PRAGMA journal_mode=OFF",
		"PRAGMA synchronous=OFF",
		"PRAGMA cache_size=-65536",
		"PRAGMA mmap_size=134217728",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		t0 := time.Now()
		if _, err := d.Exec(p); err != nil {
			fmt.Printf("pragma %s: %v\n", p, err)
		} else {
			fmt.Printf("pragma %s: %v\n", p, time.Since(t0))
		}
	}
	db = d
}

func queryWord(w string) *EcdictEntry {
	row := db.QueryRow("SELECT word, phonetic, definition, translation, pos, collins, oxford, tag, bnc, frq, exchange FROM ecdict WHERE word = ?", w)
	var e EcdictEntry
	var phonetic, definition, translation, pos, tag, exchange sql.NullString
	var collins, oxford, bnc, frq sql.NullInt64
	err := row.Scan(&e.Word, &phonetic, &definition, &translation, &pos, &collins, &oxford, &tag, &bnc, &frq, &exchange)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("query error: %v", err)
		}
		return nil
	}
	return &e
}

func queryWordCollate(w string) *EcdictEntry {
	row := db.QueryRow("SELECT word, phonetic, definition, translation, pos, collins, oxford, tag, bnc, frq, exchange FROM ecdict WHERE word = ? COLLATE NOCASE", w)
	var e EcdictEntry
	var phonetic, definition, translation, pos, tag, exchange sql.NullString
	var collins, oxford, bnc, frq sql.NullInt64
	err := row.Scan(&e.Word, &phonetic, &definition, &translation, &pos, &collins, &oxford, &tag, &bnc, &frq, &exchange)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("query error: %v", err)
		}
		return nil
	}
	return &e
}

func lookupFuzzy(w string) *EcdictEntry {
	if len(w) < 2 {
		return nil
	}
	firstChar := strings.ToLower(string(w[0]))
	rows, err := db.Query("SELECT word FROM ecdict WHERE word >= ? AND word < ? LIMIT 500", firstChar, string(firstChar[0]+1))
	if err != nil {
		log.Printf("fuzzy prefix query error: %v", err)
		return nil
	}
	defer rows.Close()

	type candidate struct {
		word string
		dist int
	}
	var best candidate
	best.dist = 3
	lowerQuery := strings.ToLower(w)

	for rows.Next() {
		var cw string
		if err := rows.Scan(&cw); err != nil {
			continue
		}
		if abs(len(cw)-len(w)) > 2 {
			continue
		}
		d := levenshtein(lowerQuery, strings.ToLower(cw))
		if d < best.dist {
			best.word = cw
			best.dist = d
		}
	}
	if best.dist <= 2 && best.word != "" {
		return queryWord(best.word)
	}
	return nil
}

func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	la, lb := len(a), len(b)
	row := make([]int, la+1)
	for i := 1; i <= la; i++ {
		row[i] = i
	}
	for j := 1; j <= lb; j++ {
		prev := row[0]
		row[0] = j
		for i := 1; i <= la; i++ {
			temp := row[i]
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			row[i] = min3(temp+1, row[i-1]+1, prev+cost)
			prev = temp
		}
	}
	return row[la]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// lookup replicates LookupEcdict from main.go step by step
func lookup(word string) (*EcdictEntry, []string) {
	var steps []string
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, steps
	}
	t := time.Now()
	e := queryWord(word)
	steps = append(steps, fmt.Sprintf("step1 exact: %v", time.Since(t)))
	if e != nil {
		return e, steps
	}
	lowerWord := strings.ToLower(word)
	if lowerWord != word {
		t = time.Now()
		e = queryWord(lowerWord)
		steps = append(steps, fmt.Sprintf("step2 lower: %v", time.Since(t)))
		if e != nil {
			return e, steps
		}
	}
	if len(lowerWord) > 0 && lowerWord[0] >= 'a' && lowerWord[0] <= 'z' {
		titleWord := strings.ToUpper(string(lowerWord[0])) + lowerWord[1:]
		if titleWord != word && titleWord != lowerWord {
			t = time.Now()
			e = queryWord(titleWord)
			steps = append(steps, fmt.Sprintf("step3 title: %v", time.Since(t)))
			if e != nil {
				return e, steps
			}
		}
	}
	if strings.Contains(word, " ") || strings.Contains(word, "-") {
		t = time.Now()
		e = queryWordCollate(word)
		steps = append(steps, fmt.Sprintf("step3b collate: %v", time.Since(t)))
		if e != nil {
			return e, steps
		}
	}
	t = time.Now()
	e = lookupFuzzy(word)
	steps = append(steps, fmt.Sprintf("step4 fuzzy: %v", time.Since(t)))
	if e != nil {
		return e, steps
	}
	return nil, steps
}

func main() {
	dbPath := os.Args[1]
	fmt.Println("DB:", dbPath)
	openDB(dbPath)
	fmt.Println()

	words := []string{
		"hello",        // exists, lowercase
		"abandon",      // exists
		"Hello",        // exists title-case → step1 fails, step2 lower hits
		"Bahai",        // proper noun title-case in DB
		"recieve",      // typo → fuzzy "receive"
		"zzzzzzzzzz",   // not exists, fuzzy miss
		"qxjklwprtz",   // not exists, fuzzy miss
		"xy",           // not exists, fuzzy miss
		"A AND NOT B gate", // multi-word → collate
		"iPhone",       // mixed case in DB
		"miscellaneous",// long word
		"the",          // common word
		"a",            // single letter
		"",             // empty
	}

	for _, w := range words {
		// warm up + measure 3 runs
		for run := 0; run < 3; run++ {
			start := time.Now()
			e, steps := lookup(w)
			elapsed := time.Since(start)
			found := "MISS"
			if e != nil {
				found = e.Word
			}
			fmt.Printf("[run%d] %-18q → %-8s total=%v\n", run, w, found, elapsed)
			if run == 2 {
				for _, s := range steps {
					fmt.Printf("        %s\n", s)
				}
			}
		}
		fmt.Println()
	}
}
