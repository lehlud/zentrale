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

// query executes a parameterized statement and decodes its rows into T.
// T can be a struct, string-keyed map, slice, or pointer to one of these types.
// Structs use db tags and case-insensitive field matching.
//
// Scalar results require exactly one row; slices receive all rows in query order.
// Empty slices and successful pointer results are non-nil. Errors return the zero
// value of T, and the result set is always closed before returning.
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

// insertOne inserts a struct or string-keyed map and decodes the complete row
// into T. Input and result types are independent; database defaults are returned.
//
// db tags rename fields, "-" skips them, and omitempty preserves database
// defaults for zero values. Identifiers are quoted and values are bound.
// Insertion and decoding share a transaction, so either failure rolls back.
func (i *Instance) insertOne[T any](table string, instance any) (T, error) {
	rows, err := i.insertMany[T](table, []any{instance})
	if err != nil {
		var zero T

		return zero, err
	}

	return rows[0], nil
}

// insertMany inserts and decodes each row in input order within one transaction.
// Rows follow the same field and identifier rules as insertOne and may supply
// different columns to use different database defaults.
//
// An empty input returns a non-nil empty result after validating the datastore
// and table name. Any insertion, decoding, or commit failure rolls back the
// entire batch and returns a nil result.
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

// update changes the supplied fields and decodes complete updated rows into T.
// Input fields follow insertOne rules; nil values write SQL NULL and omitted
// columns remain unchanged. The trusted predicate excludes WHERE and must be
// explicit; use TRUE to update every row. Its arguments follow the SET values.
//
// Rows are decoded before committing. Any failure rolls back and returns nil;
// no matches return a non-nil empty result. Result order is unspecified.
func (i *Instance) update[T any](
	table string,
	instance any,
	predicate string,
	args ...any,
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

// delete removes matching rows and returns their complete previous contents.
// The trusted predicate excludes WHERE and must be explicit; use TRUE to delete
// every row. Identifiers follow insertOne rules and argument values are bound.
//
// No matches return a non-nil empty result. Failures return nil and an error.
// Result order is unspecified.
func (i *Instance) delete(
	table string,
	predicate string,
	args ...any,
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
