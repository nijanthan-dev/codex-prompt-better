//go:build ignore

// Command generate-erd keeps the GitHub ERD and its SVG synchronized with migrations.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	startMarker = "<!-- BEGIN GENERATED ERD -->"
	endMarker   = "<!-- END GENERATED ERD -->"
	svgPath     = "docs/data-model-erd.svg"
	dbmlPath    = "docs/data-model.dbml"
)

var (
	tablePattern     = regexp.MustCompile(`(?ms)^CREATE TABLE (?:IF NOT EXISTS )?(?:prompt_better\.)?([a-z_]+) \(\n(.*?)\n\);`)
	alterPattern     = regexp.MustCompile(`(?s)ALTER TABLE (?:prompt_better\.)?([a-z_]+)\s+(.*?);`)
	addColumnPattern = regexp.MustCompile(`(?i)ADD\s+COLUMN\s+([a-z_]+)\s+(double\s+precision|timestamp\s+with\s+time\s+zone|[a-z]+(?:\([^)]*\))?)(?:\s+NOT\s+NULL)?`)
	requiredPattern  = regexp.MustCompile(`ALTER\s+([a-z_]+)\s+SET NOT NULL`)
	fkPattern        = regexp.MustCompile(`FOREIGN KEY \(([a-z_]+)\) REFERENCES ([a-z_]+)(?: \(([a-z_]+)\))?`)
)

type column struct {
	name, dataType string
	primary        bool
	required       bool
	foreign        bool
}

type table struct {
	name, domain string
	columns      []column
}

type relationship struct {
	parent, parentColumn string
	child, childColumn   string
	required             bool
}

type model struct {
	tables        []table
	relationships []relationship
	fingerprint   string
}

type point struct{ x, y int }

var domainTables = []struct {
	name   string
	tables []string
}{
	{"Identity and collection", []string{"projects", "project_versions", "sources", "source_versions", "key_versions", "project_aliases", "collection_cursors"}},
	{"Execution", []string{"sessions", "tasks", "trajectories", "turns", "responses", "items", "phases", "state_epochs", "tool_calls", "execution_events"}},
	{"Evidence", []string{"evidence_artifacts", "evidence_links", "observations"}},
	{"Governance", []string{"audit_windows", "audit_revisions", "evaluation_runs", "metric_definitions", "metric_results", "findings", "recommendations", "recommendation_events"}},
	{"Retention", []string{"retention_policies", "retention_actions", "archive_batches", "retention_action_entities"}},
}

func main() {
	check := flag.Bool("check", false, "fail instead of updating stale artifacts")
	database := flag.String("database", "", "optional PostgreSQL URL for catalog verification")
	flag.Parse()

	schemaSQL, err := readMigrationSchema("migrations")
	if err != nil {
		fail(err)
	}
	schema, err := parseModel(schemaSQL)
	if err != nil {
		fail(err)
	}
	if err := verifyParserExtensibility(schemaSQL); err != nil {
		fail(err)
	}
	if *database != "" {
		if err := verifyCatalog(context.Background(), *database, schema); err != nil {
			fail(err)
		}
		fmt.Println("PASS ERD matches PostgreSQL catalog")
	}

	document := mustRead("docs/data-model.md")
	updated, err := replaceGenerated(document, renderMarkdown(schema))
	if err != nil {
		fail(err)
	}
	svg := renderSVG(schema)
	dbml := renderDBML(schema)
	staleDocument := updated != document
	currentSVG, readErr := os.ReadFile(svgPath)
	staleSVG := readErr != nil || string(currentSVG) != svg
	currentDBML, readErr := os.ReadFile(dbmlPath)
	staleDBML := readErr != nil || string(currentDBML) != dbml
	if !staleDocument && !staleSVG && !staleDBML {
		fmt.Println("PASS generated ERD artifacts")
		return
	}
	if *check {
		fail(errors.New("ERD artifacts are stale; run go run ./scripts/generate-erd.go"))
	}
	if staleDocument {
		mustWrite("docs/data-model.md", updated)
	}
	if staleSVG {
		mustWrite(svgPath, svg)
	}
	if staleDBML {
		mustWrite(dbmlPath, dbml)
	}
	fmt.Println("UPDATED generated ERD artifacts")
}

func parseModel(schemaSQL string) (model, error) {
	domainByTable := map[string]string{}
	for _, domain := range domainTables {
		for _, name := range domain.tables {
			domainByTable[name] = domain.name
		}
	}
	result := model{}
	primaryByTable := map[string]string{}
	for _, match := range tablePattern.FindAllStringSubmatch(schemaSQL, -1) {
		parsed := table{name: match[1], domain: domainByTable[match[1]]}
		if parsed.domain == "" {
			parsed.domain = "Extensions"
		}
		for _, raw := range strings.Split(match[2], "\n") {
			line := strings.TrimSpace(strings.TrimSuffix(raw, ","))
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			dataType := parts[1]
			if len(parts) > 2 && parts[1] == "double" && parts[2] == "precision" {
				dataType = "double precision"
			}
			parsedColumn := column{name: parts[0], dataType: dataType,
				primary: strings.Contains(line, "PRIMARY KEY")}
			if parsedColumn.primary {
				parsedColumn.required = true
				primaryByTable[parsed.name] = parsedColumn.name
			}
			parsed.columns = append(parsed.columns, parsedColumn)
		}
		result.tables = append(result.tables, parsed)
	}
	if len(result.tables) == 0 {
		return model{}, errors.New("no migration tables found")
	}
	tableIndex := map[string]int{}
	columnIndex := map[string]map[string]int{}
	for i, parsed := range result.tables {
		tableIndex[parsed.name] = i
		columnIndex[parsed.name] = map[string]int{}
		for j, parsedColumn := range parsed.columns {
			columnIndex[parsed.name][parsedColumn.name] = j
		}
	}
	for _, alter := range alterPattern.FindAllStringSubmatch(schemaSQL, -1) {
		child, body := alter[1], alter[2]
		for _, match := range addColumnPattern.FindAllStringSubmatch(body, -1) {
			tablePosition, ok := tableIndex[child]
			if !ok {
				return model{}, fmt.Errorf("column migration references unknown table %s", child)
			}
			if _, exists := columnIndex[child][match[1]]; exists {
				return model{}, fmt.Errorf("column migration repeats %s.%s", child, match[1])
			}
			parsedColumn := column{name: match[1], dataType: strings.ToLower(strings.Join(strings.Fields(match[2]), " ")),
				required: strings.Contains(strings.ToUpper(match[0]), "NOT NULL")}
			result.tables[tablePosition].columns = append(result.tables[tablePosition].columns, parsedColumn)
			columnIndex[child][match[1]] = len(result.tables[tablePosition].columns) - 1
		}
		for _, match := range requiredPattern.FindAllStringSubmatch(body, -1) {
			if err := setColumnFlag(&result, tableIndex, columnIndex, child, match[1], "required"); err != nil {
				return model{}, err
			}
		}
		for _, match := range fkPattern.FindAllStringSubmatch(body, -1) {
			parentColumn := match[3]
			if parentColumn == "" {
				parentColumn = primaryByTable[match[2]]
			}
			if parentColumn == "" {
				return model{}, fmt.Errorf("foreign key parent %s has no primary key", match[2])
			}
			if err := setColumnFlag(&result, tableIndex, columnIndex, child, match[1], "foreign"); err != nil {
				return model{}, err
			}
			childColumn := result.tables[tableIndex[child]].columns[columnIndex[child][match[1]]]
			result.relationships = append(result.relationships, relationship{
				parent: match[2], parentColumn: parentColumn, child: child,
				childColumn: match[1], required: childColumn.required,
			})
		}
	}
	sort.Slice(result.relationships, func(i, j int) bool {
		left := result.relationships[i].child + "." + result.relationships[i].childColumn
		right := result.relationships[j].child + "." + result.relationships[j].childColumn
		return left < right
	})
	var canonical strings.Builder
	for _, parsed := range result.tables {
		fmt.Fprintf(&canonical, "table:%s\n", parsed.name)
		for _, parsedColumn := range parsed.columns {
			fmt.Fprintf(&canonical, "column:%s:%s:%t:%t\n", parsedColumn.name,
				parsedColumn.dataType, parsedColumn.primary, parsedColumn.required)
		}
	}
	for _, relation := range result.relationships {
		fmt.Fprintf(&canonical, "fk:%s.%s>%s.%s:%t\n", relation.child,
			relation.childColumn, relation.parent, relation.parentColumn, relation.required)
	}
	hash := sha256.Sum256([]byte(canonical.String()))
	result.fingerprint = fmt.Sprintf("%x", hash[:6])
	return result, nil
}

func verifyParserExtensibility(schemaSQL string) error {
	probeSQL := schemaSQL + `
CREATE TABLE erd_extension_probe (
    extension_id uuid PRIMARY KEY,
    project_id uuid
);
ALTER TABLE erd_extension_probe
    ADD COLUMN observed_at timestamptz NOT NULL,
    ADD CONSTRAINT erd_extension_probe_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;
`
	probe, err := parseModel(probeSQL)
	if err != nil {
		return fmt.Errorf("ERD extension self-check: %w", err)
	}
	parsed := findTable(probe, "erd_extension_probe")
	if parsed.domain != "Extensions" || len(parsed.columns) != 3 ||
		parsed.columns[2].name != "observed_at" || !parsed.columns[2].required {
		return errors.New("ERD extension self-check failed")
	}
	return nil
}

func setColumnFlag(schema *model, tables map[string]int, columns map[string]map[string]int, tableName, columnName, flagName string) error {
	tablePosition, ok := tables[tableName]
	if !ok {
		return fmt.Errorf("constraint references unknown table %s", tableName)
	}
	columnPosition, ok := columns[tableName][columnName]
	if !ok {
		return fmt.Errorf("constraint references unknown column %s.%s", tableName, columnName)
	}
	if flagName == "required" {
		schema.tables[tablePosition].columns[columnPosition].required = true
	} else {
		schema.tables[tablePosition].columns[columnPosition].foreign = true
	}
	return nil
}

func renderMarkdown(schema model) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%s\n\n_Generated from all ordered migration up-sections; schema fingerprint `%s`._\n\n",
		startMarker, schema.fingerprint)
	output.WriteString("[![Complete physical ERD with columns, types, keys, and relationships](data-model-erd.svg)](data-model-erd.svg)\n\n")
	output.WriteString("Open the SVG for a zoomable complete physical model, or edit/import [`data-model.dbml`](data-model.dbml) in a DBML-compatible editor. Both files are updated in place. The bounded diagrams below remain readable in GitHub.\n")
	for _, domain := range domainsForModel(schema) {
		fmt.Fprintf(&output, "\n### %s\n\n```mermaid\nerDiagram\n", domain.name)
		included := map[string]bool{}
		for _, name := range domain.tables {
			included[name] = true
			renderMermaidTable(&output, findTable(schema, name))
		}
		for _, relation := range schema.relationships {
			if !included[relation.child] {
				continue
			}
			cardinality := "o|--o{"
			if relation.required {
				cardinality = "||--o{"
			}
			fmt.Fprintf(&output, "    %s %s %s : %q\n", relation.parent, cardinality,
				relation.child, relation.parentColumn+" to "+relation.childColumn)
		}
		output.WriteString("```\n")
	}
	fmt.Fprintf(&output, "\n%s", endMarker)
	return output.String()
}

func renderDBML(schema model) string {
	var output strings.Builder
	fmt.Fprintf(&output, "// Generated from all ordered migration up-sections.\n// Schema fingerprint: %s\n// Edit migrations, then run: go run ./scripts/generate-erd.go\n\n",
		schema.fingerprint)
	for _, parsed := range schema.tables {
		fmt.Fprintf(&output, "Table %s {\n", parsed.name)
		for _, parsedColumn := range parsed.columns {
			settings := []string{}
			if parsedColumn.primary {
				settings = append(settings, "pk")
			}
			if parsedColumn.required {
				settings = append(settings, "not null")
			}
			suffix := ""
			if len(settings) > 0 {
				suffix = " [" + strings.Join(settings, ", ") + "]"
			}
			fmt.Fprintf(&output, "  %s %s%s\n", parsedColumn.name, parsedColumn.dataType, suffix)
		}
		output.WriteString("}\n\n")
	}
	for _, relation := range schema.relationships {
		fmt.Fprintf(&output, "Ref: %s.%s > %s.%s\n", relation.child, relation.childColumn,
			relation.parent, relation.parentColumn)
	}
	return output.String()
}

func renderMermaidTable(output *strings.Builder, parsed table) {
	fmt.Fprintf(output, "    %s {\n", parsed.name)
	for _, parsedColumn := range parsed.columns {
		flags := []string{}
		if parsedColumn.primary {
			flags = append(flags, "PK")
		}
		if parsedColumn.foreign {
			flags = append(flags, "FK")
		}
		dataType := strings.ReplaceAll(parsedColumn.dataType, " ", "_")
		suffix := ""
		if len(flags) > 0 {
			suffix = " " + strings.Join(flags, ",")
		}
		fmt.Fprintf(output, "        %s %s%s\n", dataType, parsedColumn.name, suffix)
	}
	output.WriteString("    }\n")
}

func renderSVG(schema model) string {
	const (
		margin      = 30
		columnWidth = 500
		domainGap   = 70
		rowHeight   = 19
		tableHeader = 28
		tableGap    = 24
	)
	colors := []string{"#2563eb", "#7c3aed", "#0891b2", "#c2410c", "#15803d", "#be123c"}
	domains := domainsForModel(schema)
	positions := map[string]map[string]point{}
	tableOrigins := map[string]point{}
	height := 0
	for domainIndex, domain := range domains {
		y := margin + 52
		x := margin + domainIndex*(columnWidth+domainGap)
		for _, name := range domain.tables {
			parsed := findTable(schema, name)
			tableOrigins[name] = point{x, y}
			positions[name] = map[string]point{}
			for columnIndex, parsedColumn := range parsed.columns {
				positions[name][parsedColumn.name] = point{x + columnWidth, y + tableHeader + columnIndex*rowHeight + rowHeight/2}
			}
			y += tableHeader + len(parsed.columns)*rowHeight + tableGap
		}
		if y > height {
			height = y
		}
	}
	width := margin*2 + len(domains)*columnWidth + (len(domains)-1)*domainGap
	var output strings.Builder
	fmt.Fprintf(&output, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="title desc">`, width, height+margin, width, height+margin)
	output.WriteString("\n<title id=\"title\">Prompt Better PostgreSQL physical entity relationship diagram</title>\n")
	fmt.Fprintf(&output, "<desc id=\"desc\">All %d tables, columns, PostgreSQL data types, primary keys, foreign keys, and column relationships.</desc>\n", len(schema.tables))
	fmt.Fprintf(&output, "<metadata>schema-fingerprint:%s</metadata>\n", schema.fingerprint)
	output.WriteString(`<rect width="100%" height="100%" fill="#f8fafc"/>` + "\n")
	for _, relation := range schema.relationships {
		from := positions[relation.parent][relation.parentColumn]
		to := positions[relation.child][relation.childColumn]
		if from == (point{}) || to == (point{}) {
			continue
		}
		fromOrigin, toOrigin := tableOrigins[relation.parent], tableOrigins[relation.child]
		if fromOrigin.x > toOrigin.x {
			from.x = fromOrigin.x
			to.x = toOrigin.x + columnWidth
		}
		mid := (from.x + to.x) / 2
		fmt.Fprintf(&output, `<path d="M%d %d C%d %d,%d %d,%d %d" fill="none" stroke="#64748b" stroke-width="1" opacity="0.38"><title>%s.%s to %s.%s</title></path>`+"\n",
			from.x, from.y, mid, from.y, mid, to.y, to.x, to.y,
			html.EscapeString(relation.parent), html.EscapeString(relation.parentColumn), html.EscapeString(relation.child), html.EscapeString(relation.childColumn))
	}
	for domainIndex, domain := range domains {
		x := margin + domainIndex*(columnWidth+domainGap)
		fmt.Fprintf(&output, `<text x="%d" y="%d" font-family="ui-sans-serif,system-ui" font-size="22" font-weight="700" fill="%s">%s</text>`+"\n",
			x, margin+20, colors[domainIndex], html.EscapeString(domain.name))
		for _, name := range domain.tables {
			parsed := findTable(schema, name)
			origin := tableOrigins[name]
			tableHeight := tableHeader + len(parsed.columns)*rowHeight
			fmt.Fprintf(&output, `<rect x="%d" y="%d" width="%d" height="%d" rx="6" fill="white" stroke="#cbd5e1"/>`+"\n", origin.x, origin.y, columnWidth, tableHeight)
			fmt.Fprintf(&output, `<path d="M%d %d h%d v%d h-%d z" fill="%s"/>`+"\n", origin.x, origin.y+6, columnWidth, tableHeader-6, columnWidth, colors[domainIndex])
			fmt.Fprintf(&output, `<text x="%d" y="%d" font-family="ui-monospace,SFMono-Regular" font-size="14" font-weight="700" fill="white">%s</text>`+"\n", origin.x+10, origin.y+19, html.EscapeString(name))
			for columnIndex, parsedColumn := range parsed.columns {
				y := origin.y + tableHeader + columnIndex*rowHeight
				if columnIndex%2 == 1 {
					fmt.Fprintf(&output, `<rect x="%d" y="%d" width="%d" height="%d" fill="#f1f5f9"/>`+"\n", origin.x+1, y, columnWidth-2, rowHeight)
				}
				flags := ""
				if parsedColumn.primary {
					flags = "PK"
				}
				if parsedColumn.foreign {
					if flags != "" {
						flags += " "
					}
					flags += "FK"
				}
				if parsedColumn.required && !parsedColumn.primary {
					flags += " NN"
				}
				fmt.Fprintf(&output, `<text x="%d" y="%d" font-family="ui-monospace,SFMono-Regular" font-size="11" fill="#0f172a">%s</text>`+"\n", origin.x+10, y+13, html.EscapeString(parsedColumn.name))
				fmt.Fprintf(&output, `<text x="%d" y="%d" font-family="ui-monospace,SFMono-Regular" font-size="11" fill="#475569">%s</text>`+"\n", origin.x+285, y+13, html.EscapeString(parsedColumn.dataType))
				fmt.Fprintf(&output, `<text x="%d" y="%d" font-family="ui-monospace,SFMono-Regular" font-size="10" font-weight="700" fill="%s">%s</text>`+"\n", origin.x+420, y+13, colors[domainIndex], html.EscapeString(strings.TrimSpace(flags)))
			}
		}
	}
	output.WriteString("</svg>\n")
	return output.String()
}

func readMigrationSchema(directory string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "*.sql"))
	if err != nil || len(paths) == 0 {
		return "", errors.New("migration files unavailable")
	}
	sort.Strings(paths)
	var schema strings.Builder
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read migration %s", filepath.Base(path))
		}
		up := strings.SplitN(string(content), "-- +goose Down", 2)[0]
		schema.WriteString(up)
		schema.WriteByte('\n')
	}
	return schema.String(), nil
}

func domainsForModel(schema model) []struct {
	name   string
	tables []string
} {
	present := map[string]bool{}
	for _, parsed := range schema.tables {
		present[parsed.name] = true
	}
	result := make([]struct {
		name   string
		tables []string
	}, 0, len(domainTables)+1)
	assigned := map[string]bool{}
	for _, domain := range domainTables {
		filtered := []string{}
		for _, name := range domain.tables {
			if present[name] {
				filtered = append(filtered, name)
				assigned[name] = true
			}
		}
		if len(filtered) > 0 {
			result = append(result, struct {
				name   string
				tables []string
			}{domain.name, filtered})
		}
	}
	extensions := []string{}
	for _, parsed := range schema.tables {
		if !assigned[parsed.name] {
			extensions = append(extensions, parsed.name)
		}
	}
	sort.Strings(extensions)
	if len(extensions) > 0 {
		result = append(result, struct {
			name   string
			tables []string
		}{"Extensions", extensions})
	}
	return result
}

func verifyCatalog(ctx context.Context, dsn string, schema model) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("open catalog verification database")
	}
	defer db.Close()
	expectedColumns := map[string]bool{}
	for _, parsed := range schema.tables {
		for _, parsedColumn := range parsed.columns {
			expectedColumns[parsed.name+"."+parsedColumn.name+":"+parsedColumn.dataType+fmt.Sprint(":", parsedColumn.required)] = true
		}
	}
	actualColumns := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT c.relname,a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
		WHERE n.nspname='prompt_better' AND c.relkind='r' ORDER BY c.relname,a.attnum`)
	if err != nil {
		return errors.New("read catalog columns")
	}
	for rows.Next() {
		var tableName, columnName, dataType string
		var required bool
		if err := rows.Scan(&tableName, &columnName, &dataType, &required); err != nil {
			rows.Close()
			return errors.New("scan catalog columns")
		}
		if dataType == "timestamp with time zone" {
			dataType = "timestamptz"
		}
		actualColumns[tableName+"."+columnName+":"+dataType+fmt.Sprint(":", required)] = true
	}
	rows.Close()
	if err := equalSets("columns", expectedColumns, actualColumns); err != nil {
		return err
	}
	expectedFKs := map[string]bool{}
	for _, relation := range schema.relationships {
		expectedFKs[relation.child+"."+relation.childColumn+">"+relation.parent+"."+relation.parentColumn] = true
	}
	actualFKs := map[string]bool{}
	rows, err = db.QueryContext(ctx, `SELECT tc.table_name,kcu.column_name,ccu.table_name,ccu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu ON tc.constraint_name=kcu.constraint_name AND tc.constraint_schema=kcu.constraint_schema
		JOIN information_schema.constraint_column_usage ccu ON tc.constraint_name=ccu.constraint_name AND tc.constraint_schema=ccu.constraint_schema
		WHERE tc.constraint_schema='prompt_better' AND tc.constraint_type='FOREIGN KEY'`)
	if err != nil {
		return errors.New("read catalog foreign keys")
	}
	for rows.Next() {
		var child, childColumn, parent, parentColumn string
		if err := rows.Scan(&child, &childColumn, &parent, &parentColumn); err != nil {
			rows.Close()
			return errors.New("scan catalog foreign keys")
		}
		actualFKs[child+"."+childColumn+">"+parent+"."+parentColumn] = true
	}
	rows.Close()
	return equalSets("foreign keys", expectedFKs, actualFKs)
}

func equalSets(name string, expected, actual map[string]bool) error {
	missing, unexpected := []string{}, []string{}
	for value := range expected {
		if !actual[value] {
			missing = append(missing, value)
		}
	}
	for value := range actual {
		if !expected[value] {
			unexpected = append(unexpected, value)
		}
	}
	if len(missing) == 0 && len(unexpected) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return fmt.Errorf("ERD %s mismatch: missing=%v unexpected=%v", name, missing, unexpected)
}

func findTable(schema model, name string) table {
	for _, parsed := range schema.tables {
		if parsed.name == name {
			return parsed
		}
	}
	return table{}
}

func replaceGenerated(document, generated string) (string, error) {
	start := strings.Index(document, startMarker)
	end := strings.Index(document, endMarker)
	if start < 0 || end < start {
		return "", errors.New("generated ERD markers are missing from docs/data-model.md")
	}
	end += len(endMarker)
	return document[:start] + generated + document[end:], nil
}

func mustRead(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	return string(content)
}

func mustWrite(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
