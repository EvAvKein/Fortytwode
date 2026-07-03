// schema_prettify reads pg_dump --schema-only output from stdin and writes
// compact SQL to stdout. Strips session settings, pg_dump metadata, and
// collapses sequences into SERIAL with inlined constraints. Standalone indexes
// are kept (grouped after the tables) — some carry semantics the tables alone
// don't show, e.g. the case-insensitive unique index on accounts(LOWER(email)).
//
// Usage: pg_dump ... | go run ./internal/store/schema_prettify
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

type constraint struct {
	serial, unique, primaryKey bool
	fk                         string // e.g. "accounts(id) ON DELETE CASCADE"
}

// errWriter wraps an io.Writer and captures the first write error,
// eliminating repetitive if-err checks at every Fprint call.
type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) WriteString(s string) {
	if ew.err != nil {
		return
	}
	_, ew.err = fmt.Fprint(ew.w, s)
}

var (
	reAlterStart  = regexp.MustCompile(`^ALTER TABLE ONLY ([a-z_.]+)`)
	reAlterSerial = regexp.MustCompile(`ALTER TABLE ONLY ([a-z_.]+) ALTER COLUMN ([a-z_]+) SET DEFAULT nextval`)
	reAddUnique   = regexp.MustCompile(`ADD CONSTRAINT .+ UNIQUE \(([a-z_]+)\)`)
	reAddPkey     = regexp.MustCompile(`ADD CONSTRAINT .+ PRIMARY KEY \(([a-z_]+)\)`)
	reAddFK       = regexp.MustCompile(`ADD CONSTRAINT .+ FOREIGN KEY \(([a-z_]+)\) REFERENCES ([a-z_.]+)\(([a-z_]+)\) ON DELETE (.+)`)
	reCreateTable = regexp.MustCompile(`^CREATE TABLE ([a-z_.]+) \(`)
	reIsHeader    = regexp.MustCompile(`^-- (REFERENCE ONLY|of truth)`)
	reIsSeq       = regexp.MustCompile(`^CREATE SEQUENCE `)
	reIsAltSeq    = regexp.MustCompile(`^ALTER SEQUENCE `)
	reIsAlt       = regexp.MustCompile(`^ALTER TABLE `)
	reIsAddCon    = regexp.MustCompile(`^    ADD CONSTRAINT`)
	reIsDash      = regexp.MustCompile(`^--$`)
	reIsComment   = regexp.MustCompile(`^--`)
	reEndParen    = regexp.MustCompile(`^\);`)
	reIsIndex     = regexp.MustCompile(`^CREATE (UNIQUE )?INDEX `)
)

func main() {
	lines := readLines(os.Stdin)
	cons := collectConstraints(lines)
	if err := writePrettified(os.Stdout, lines, cons); err != nil {
		fmt.Fprintf(os.Stderr, "error writing output: %v\n", err)
		os.Exit(1)
	}
}

func readLines(r io.Reader) []string {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error reading input: %v\n", err)
		os.Exit(1)
	}
	return lines
}

// ensureConstraint returns the constraint for table/col, creating
// intermediate maps/structs as needed.
func ensureConstraint(cons map[string]map[string]*constraint, table, col string) *constraint {
	if cons[table] == nil {
		cons[table] = make(map[string]*constraint)
	}
	if cons[table][col] == nil {
		cons[table][col] = &constraint{}
	}
	return cons[table][col]
}

func collectConstraints(lines []string) map[string]map[string]*constraint {
	cons := make(map[string]map[string]*constraint)
	curTable := ""

	for _, line := range lines {
		if m := reAlterSerial.FindStringSubmatch(line); m != nil {
			table := strings.ReplaceAll(m[1], "public.", "")
			ensureConstraint(cons, table, m[2]).serial = true
			curTable = ""
			continue
		}

		if m := reAlterStart.FindStringSubmatch(line); m != nil {
			curTable = strings.ReplaceAll(m[1], "public.", "")
			continue
		}

		if curTable != "" {
			if m := reAddUnique.FindStringSubmatch(line); m != nil {
				ensureConstraint(cons, curTable, m[1]).unique = true
				curTable = ""
				continue
			}
			if m := reAddPkey.FindStringSubmatch(line); m != nil {
				ensureConstraint(cons, curTable, m[1]).primaryKey = true
				curTable = ""
				continue
			}
			if m := reAddFK.FindStringSubmatch(line); m != nil {
				ref := strings.ReplaceAll(m[2], "public.", "")
				cs := ensureConstraint(cons, curTable, m[1])
				cs.fk = ref + "(" + m[3] + ") ON DELETE " + strings.TrimRight(m[4], ";")
				curTable = ""
				continue
			}
		}

		if !reAlterStart.MatchString(line) {
			curTable = ""
		}
	}
	return cons
}

// columnLine builds a single column definition with inlined constraints, without a
// trailing comma or newline. The caller joins columns with commas so the last column
// of a table gets none (a trailing comma before ");" is invalid SQL).
func columnLine(colName, colDef string, cs *constraint) string {
	if cs != nil && cs.serial {
		if cs.primaryKey {
			return "    " + colName + " SERIAL PRIMARY KEY"
		}
		return "    " + colName + " SERIAL NOT NULL"
	}

	suffix := ""
	if cs != nil && cs.unique {
		suffix += " UNIQUE"
	}
	if cs != nil && cs.primaryKey {
		suffix += " PRIMARY KEY"
	}
	if cs != nil && cs.fk != "" {
		suffix += " REFERENCES " + cs.fk
	}

	if strings.Contains(suffix, "PRIMARY KEY") {
		colDef = strings.TrimSuffix(colDef, " NOT NULL")
	}
	return "    " + colName + " " + colDef + suffix
}

func writePrettified(w io.Writer, lines []string, cons map[string]map[string]*constraint) error {
	ew := &errWriter{w: w}
	inCreate := false
	currentTable := ""
	prevEnd := false
	var cols []string    // columns of the current table, joined with commas at ");"
	var indexes []string // standalone CREATE [UNIQUE] INDEX statements, emitted after the tables

	for _, line := range lines {
		switch {
		case reIsHeader.MatchString(line):
			ew.WriteString(line + "\n")

		case reIsIndex.MatchString(line):
			cleaned := strings.ReplaceAll(line, "public.", "")
			cleaned = strings.ReplaceAll(cleaned, " USING btree", "")
			indexes = append(indexes, cleaned)

		case reIsSeq.MatchString(line), reIsAltSeq.MatchString(line),
			reIsAlt.MatchString(line), reIsAddCon.MatchString(line),
			reIsDash.MatchString(line), reIsComment.MatchString(line):
			// skip pg_dump noise

		case reCreateTable.MatchString(line):
			if prevEnd {
				ew.WriteString("\n")
			}
			inCreate = true
			m := reCreateTable.FindStringSubmatch(line)
			currentTable = strings.ReplaceAll(m[1], "public.", "")
			ew.WriteString("CREATE TABLE " + currentTable + " (\n")
			cols = cols[:0]
			prevEnd = false

		case inCreate && reEndParen.MatchString(line):
			ew.WriteString(strings.Join(cols, ",\n") + "\n")
			ew.WriteString(");\n")
			inCreate = false
			currentTable = ""
			prevEnd = true

		case inCreate:
			c := strings.TrimSpace(strings.TrimSuffix(line, ","))
			parts := strings.SplitN(c, " ", 2)
			if len(parts) < 2 {
				fmt.Fprintf(os.Stderr, "warning: skipping malformed column line: %s\n", line)
				break
			}
			var cs *constraint
			if tc := cons[currentTable]; tc != nil {
				cs = tc[parts[0]]
			}
			cols = append(cols, columnLine(parts[0], parts[1], cs))
		}

		if ew.err != nil {
			return ew.err
		}
	}
	for _, idx := range indexes {
		ew.WriteString("\n" + idx + "\n")
	}
	return ew.err
}
