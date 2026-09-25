package db

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s*\((.*?)\n\);`)
	addColumnRe   = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+([a-z_][a-z0-9_]*)\s+ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	sqlKeywords   = map[string]bool{"primary": true, "unique": true, "constraint": true, "foreign": true, "check": true}
)

func schemaOf(sql string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, m := range createTableRe.FindAllStringSubmatch(sql, -1) {
		cols := map[string]bool{}
		for _, line := range strings.Split(m[2], "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
			name := strings.ToLower(strings.Fields(line)[0])
			if !sqlKeywords[name] {
				cols[name] = true
			}
		}
		out[strings.ToLower(m[1])] = cols
	}
	for _, m := range addColumnRe.FindAllStringSubmatch(sql, -1) {
		t := strings.ToLower(m[1])
		if out[t] == nil {
			out[t] = map[string]bool{}
		}
		out[t][strings.ToLower(m[2])] = true
	}
	return out
}

func upSections(t *testing.T) string {
	t.Helper()
	files, _ := filepath.Glob("migrations/*.sql")
	if len(files) == 0 {
		t.Fatal("relay has no goose migrations")
	}
	sort.Strings(files)
	var b strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		if !strings.Contains(s, "-- +goose Up") || !strings.Contains(s, "-- +goose Down") {
			t.Fatalf("%s: missing goose Up/Down markers", f)
		}
		b.WriteString(strings.SplitN(strings.SplitN(s, "-- +goose Up", 2)[1], "-- +goose Down", 2)[0])
		b.WriteString("\n")
	}
	return b.String()
}

// Relay's embedded init.sql and its goose migrations must define the same
// tables and columns, so the goose path never lags the bootstrap path.
func TestInitSQLAndMigrationsDefineSameSchema(t *testing.T) {
	initSchema := schemaOf(schemaSQL)
	mig := schemaOf(upSections(t))
	if len(initSchema) == 0 {
		t.Fatal("parsed no tables from init.sql")
	}
	for table, cols := range initSchema {
		mcols, ok := mig[table]
		if !ok {
			t.Errorf("table %s is in init.sql but has no goose migration", table)
			continue
		}
		for c := range cols {
			if !mcols[c] {
				t.Errorf("column %s.%s is in init.sql but not in migrations", table, c)
			}
		}
	}
	for table := range mig {
		if _, ok := initSchema[table]; !ok {
			t.Errorf("table %s is migrated but missing from init.sql", table)
		}
	}
}
