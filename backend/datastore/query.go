package datastore

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-viper/mapstructure/v2"
)

var errClosed = errors.New("datastore is closed")

// generic (private) query helper; T may be a struct, a string-keyed map,
// a slice of either, or a pointer to one of these types. The function executes
// the statement and decodes its result into T. Values for ? placeholders are
// supplied through args rather than interpolated into the SQL statement.
//
// Exported struct fields use their db tags, or their case-insensitive field
// names when no tag is present. A non-slice result requires exactly one row;
// a slice receives all rows in query order, including a non-nil empty slice
// when no rows match. Pointer results are allocated on success.
//
// shall error, if:
// (1) the datastore instance is nil, uninitialized, or already closed
// (2) executing the query, reading its columns, or scanning its rows fails
// (3) a non-slice result has no rows (sql.ErrNoRows) or more than one row
// (4) the returned values cannot be decoded into T
//
// On failure, the returned result is the zero value of T. On success, maps,
// slices, and pointers are non-nil. The result set is closed before returning,
// including on failure, so it does not hold a database connection open.
func (i *Instance) query[T any](statement string, args ...any) (T, error) {
	var result T
	if i == nil || i.Closed() {
		return result, errClosed
	}

	rows, err := queryRows(i.db, statement, args...)
	if err != nil {
		return result, err
	}

	resultType := reflect.TypeFor[T]()
	for resultType != nil && resultType.Kind() == reflect.Pointer {
		resultType = resultType.Elem()
	}

	var input any = rows
	if resultType == nil || resultType.Kind() != reflect.Slice {
		if len(rows) == 0 {
			return result, sql.ErrNoRows
		}

		if len(rows) != 1 {
			return result, fmt.Errorf("query: expected one row, got %d", len(rows))
		}

		input = rows[0]
	}

	result, err = decodeResult[T](input)
	if err != nil {
		return result, fmt.Errorf("query: %w", err)
	}

	return result, nil
}

// generic (private) insert helper; T selects the returned row type independently
// of instance. T may be a struct, a string-keyed map, or a pointer to either,
// decoded with the same db tags and field matching rules as query. For example,
// insertOne[SSHPrivKey](table, map[string]any{...}) returns a typed SSHPrivKey
// containing database defaults and generated columns.
//
// The input instance may be any type whose structure we can meaningfully reason
// about: a struct, a non-nil string-keyed map, or a non-nil pointer to either.
//
// Exported struct fields use their db tags, or their field names when no tag
// is present. A db:"-" tag skips a field; db:"name,omitempty" omits a zero
// value so database defaults can apply. Map keys are column names, and nil
// values represent SQL NULL. An empty row uses INSERT DEFAULT VALUES.
// Table names may be qualified (e.g. main.items); table and column names are
// quoted as identifiers, and field values are bound as query parameters.
//
// shall error, if:
// (1) the datastore instance is nil, uninitialized, or already closed
// (2) the row has an unsupported type, is nil, or has invalid column names
// (3) the table name is invalid or the insert fails
// (4) reading or decoding the inserted row into T fails
// (5) committing the transaction fails
//
// On failure, the result is the zero value of T and the insert is rolled back,
// including when decoding fails. On success, maps and pointers are non-nil.
// No partially inserted or decoded result is returned.
func (i *Instance) insertOne[T any](table string, instance any) (T, error) {
	rows, err := i.insertMany[T](table, []any{instance})
	if err != nil {
		var zero T
		return zero, err
	}

	return rows[0], nil
}

// generic (private) batch insert helper; T selects the returned row type, and U
// is the input element type, inferred from instances. Each instance follows the
// same type, field, tag, identifier, and value rules as insertOne. Input rows may
// supply different columns, allowing database defaults to apply independently
// to each row. For example, insertMany[SSHPrivKey](table, []map[string]any{...})
// returns []SSHPrivKey without requiring callers to supply complete rows.
//
// All rows are inserted in input order within one transaction. The returned
// slice follows that order and contains each complete inserted row, including
// defaults and generated columns. An empty or nil input slice succeeds with
// a non-nil empty result, provided the datastore is open and the table name
// is a valid identifier; an empty batch does not query the database.
//
// shall error, if:
// (1) the datastore instance is nil, uninitialized, or already closed
// (2) the table name or any input row is invalid
// (3) starting the transaction, inserting any row, or reading its result fails
// (4) any returned row cannot be decoded into T
// (5) committing the transaction fails
//
// Any failure rolls back the entire batch and returns a nil slice and an error.
// All rows are decoded before committing, so a decoding failure also rolls back
// rows that were already inserted and decoded successfully.
// If the returned error is nil, the returned slice must not be nil and vice versa.
func (i *Instance) insertMany[T, U any](table string, instances []U) ([]T, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}

	tableName, err := quoteTable(table)
	if err != nil {
		return nil, err
	}

	result := make([]T, 0, len(instances))
	if len(instances) == 0 {
		return result, nil
	}

	tx, err := i.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("insert: begin transaction: %w", err)
	}

	defer tx.Rollback()

	for index, instance := range instances {
		columns, values, err := rowValues(instance)
		if err != nil {
			return nil, fmt.Errorf("insert row %d: %w", index, err)
		}

		statement := "INSERT INTO " + tableName + " DEFAULT VALUES RETURNING *"
		if len(columns) > 0 {
			placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", ")
			statement = "INSERT INTO " + tableName + " (" + strings.Join(columns, ", ") +
				") VALUES (" + placeholders + ") RETURNING *"
		}

		rows, err := queryRows(tx, statement, values...)
		if err != nil {
			return nil, fmt.Errorf("insert row %d: %w", index, err)
		}

		if len(rows) != 1 {
			return nil, fmt.Errorf("insert row %d: expected one returned row", index)
		}

		row, err := decodeResult[T](rows[0])
		if err != nil {
			return nil, fmt.Errorf("insert row %d: %w", index, err)
		}

		result = append(result, row)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("insert: commit transaction: %w", err)
	}

	return result, nil
}

// generic (private) update helper; T selects the returned row type independently
// of instance and follows the same decoding rules as insertOne. The input
// instance supplies the columns to change and follows the same type, field,
// tag, identifier, and value rules as insertOne.
// Supplied zero values are written unless their fields use omitempty; nil map
// values set columns to SQL NULL. Columns absent from instance are left alone.
//
// The predicate is a trusted SQL expression without WHERE. Values for its ?
// placeholders are bound from args, after the values used for SET. A non-empty
// predicate is required; use TRUE explicitly to update every row in the table.
// The function returns the complete updated rows as []T. Their order is
// unspecified; no matches succeed with a non-nil empty slice. For example,
// update[SSHPrivKey](table, map[string]any{...}, predicate, args...) returns typed
// rows while allowing the input to contain only the columns being changed.
//
// shall error, if:
// (1) the datastore instance is nil, uninitialized, or already closed
// (2) the table name or input row is invalid, or no fields remain to update
// (3) the predicate is empty or contains only whitespace
// (4) executing the update or reading the returned rows fails
// (5) any returned row cannot be decoded into T, or committing the update fails
//
// The update is committed only after all returned rows have been decoded.
// Any failure rolls back the update and returns a nil slice and an error.
// If the returned error is nil, the returned slice must not be nil and vice versa.
func (i *Instance) update[T any](
	table string, instance any, predicate string, args ...any,
) ([]T, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}

	if strings.TrimSpace(predicate) == "" {
		return nil, errors.New("update: an explicit predicate is required")
	}

	tableName, err := quoteTable(table)
	if err != nil {
		return nil, err
	}

	columns, values, err := rowValues(instance)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}

	if len(columns) == 0 {
		return nil, errors.New("update: no fields supplied")
	}

	assignments := make([]string, len(columns))
	for index, column := range columns {
		assignments[index] = column + " = ?"
	}

	statement := "UPDATE " + tableName + " SET " + strings.Join(assignments, ", ") +
		" WHERE " + predicate + " RETURNING *"

	tx, err := i.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("update: begin transaction: %w", err)
	}

	defer tx.Rollback()

	rows, err := queryRows(tx, statement, append(values, args...)...)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}

	result, err := decodeResult[[]T](rows)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("update: commit transaction: %w", err)
	}

	return result, nil
}

// private delete helper; deletes matching rows and returns their complete
// contents as they were before deletion. Table names follow the identifier
// rules of insertOne. The predicate is trusted SQL without WHERE, with values
// for ? placeholders bound from args. It must be explicit; use TRUE to delete
// every row in the table.
//
// The returned row order is unspecified. No matches succeed with a non-nil
// empty slice.
//
// shall error, if:
// (1) the datastore instance is nil, uninitialized, or already closed
// (2) the table name is invalid
// (3) the predicate is empty or contains only whitespace
// (4) executing the delete or reading the returned rows fails
//
// If the returned error is nil, the returned slice must not be nil and vice versa.
func (i *Instance) delete(
	table string, predicate string, args ...any,
) ([]map[string]any, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}

	if strings.TrimSpace(predicate) == "" {
		return nil, errors.New("delete: an explicit predicate is required")
	}

	tableName, err := quoteTable(table)
	if err != nil {
		return nil, err
	}

	statement := "DELETE FROM " + tableName + " WHERE " + predicate + " RETURNING *"
	return queryRows(i.db, statement, args...)
}

type rowQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

// decodeResult converts a row map or slice of row maps into the requested result
// type. Query, insert, and update share this decoder so db tags and field name
// matching behave consistently. Struct fields must be exported to be decoded.
// Decoding failures return the zero value of T rather than a partially populated
// result; callers that mutate data must decode before committing their transaction.
func decodeResult[T any](input any) (T, error) {
	var result T
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:  &result,
		TagName: "db",
	})
	if err != nil {
		return result, fmt.Errorf("create decoder: %w", err)
	}

	if err := decoder.Decode(input); err != nil {
		var zero T
		return zero, fmt.Errorf("decode result: %w", err)
	}

	return result, nil
}

// queryRows executes a statement against a database or transaction and reads
// each row into a map keyed by column name. Values retain their driver types,
// including []byte, time.Time, and nil for SQL NULL. The result set is owned by
// this function and closed on every return path.
//
// Success returns a non-nil slice in result order, even when no rows match.
// Query, column, scan, and iteration failures return nil and a wrapped error;
// partially read rows are never returned.
func queryRows(db rowQuerier, statement string, args ...any) ([]map[string]any, error) {
	rows, err := db.Query(statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("query columns: %w", err)
	}

	result := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}

		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("query scan: %w", err)
		}

		row := make(map[string]any, len(columns))
		for index, column := range columns {
			row[column] = values[index]
		}

		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query rows: %w", err)
	}

	return result, nil
}

// rowValues extracts columns and values from a struct, a string-keyed map,
// or a non-nil pointer to either. Only exported struct fields participate;
// db tags rename fields, "-" skips fields, and omitempty skips zero values.
// An empty struct or map is valid and produces no columns or values.
//
// Column names are sorted and quoted as SQL identifiers. Values use the same
// order and retain their original types so database/sql can handle blobs,
// times, NULLs, and driver.Valuer implementations without a lossy conversion.
// Nil rows, unsupported types, duplicate struct column names, and invalid
// identifiers return an error and nil slices.
func rowValues(instance any) ([]string, []any, error) {
	value := reflect.ValueOf(instance)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		value = value.Elem()
	}

	if !value.IsValid() {
		return nil, nil, errors.New("row must not be nil")
	}

	fields := make(map[string]any)
	switch value.Kind() {
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String || value.IsNil() {
			return nil, nil, errors.New("row must be a non-nil string-keyed map")
		}

		iterator := value.MapRange()
		for iterator.Next() {
			fields[iterator.Key().String()] = iterator.Value().Interface()
		}

	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if !field.IsExported() {
				continue
			}

			tag := strings.Split(field.Tag.Get("db"), ",")
			name := tag[0]
			if name == "-" {
				continue
			}

			if name == "" {
				name = field.Name
			}

			omitEmpty := false
			for _, option := range tag[1:] {
				if option == "omitempty" && value.Field(index).IsZero() {
					omitEmpty = true
				}
			}

			if omitEmpty {
				continue
			}

			if _, exists := fields[name]; exists {
				return nil, nil, fmt.Errorf("duplicate column %q", name)
			}

			fields[name] = value.Field(index).Interface()
		}

	default:
		return nil, nil, fmt.Errorf("row must be a struct or string-keyed map, got %T", instance)
	}

	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}

	sort.Strings(names)

	columns := make([]string, len(names))
	values := make([]any, len(names))
	for index, name := range names {
		column, err := quoteIdentifier(name)
		if err != nil {
			return nil, nil, err
		}

		columns[index] = column
		values[index] = fields[name]
	}

	return columns, values, nil
}

// quoteTable quotes each part of a dot-qualified table name independently.
// The input is an unquoted name; dots separate catalog, schema, and table parts.
// Empty parts or NUL bytes return an error and an empty string.
func quoteTable(table string) (string, error) {
	parts := strings.Split(table, ".")
	for index, part := range parts {
		quoted, err := quoteIdentifier(part)
		if err != nil {
			return "", err
		}

		parts[index] = quoted
	}

	return strings.Join(parts, "."), nil
}

// quoteIdentifier quotes a literal SQL identifier, escaping embedded quotes.
// An empty name or a name containing NUL bytes returns an error and an empty
// string. Values must be bound as parameters rather than passed through here.
func quoteIdentifier(identifier string) (string, error) {
	if identifier == "" || strings.ContainsRune(identifier, '\x00') {
		return "", errors.New("SQL identifiers must be non-empty and contain no NUL bytes")
	}

	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`, nil
}
