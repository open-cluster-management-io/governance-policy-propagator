package complianceeventsapi

import (
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- for convenience, not cryptography
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"k8s.io/utils/strings/slices"
)

const (
	complianceEventsTableName = "compliance_events"
	clustersTableName         = "clusters"
	parentPolicyTableName     = "parent_policies"
	policyTableName           = "policies"
)

var (
	errRequiredFieldNotProvided = errors.New("required field not provided")
	errInvalidInput             = errors.New("invalid input")
)

type dbRow interface {
	FromRow(*sql.Rows) error
	GetOrCreate(ctx context.Context, db *sql.DB) error
	GetTableName() string
	InsertQuery() (string, []any)
	InsertQueryFromFields(map[string]dbField) (string, []any)
}

type ComplianceEvent struct {
	Cluster      Cluster       `json:"cluster"`
	Event        EventDetails  `json:"event"`
	ParentPolicy *ParentPolicy `json:"parent_policy,omitempty"` //nolint:tagliatelle
	Policy       Policy        `json:"policy"`
}

func (ce ComplianceEvent) Validate() error {
	errs := make([]error, 0)

	if err := ce.Cluster.Validate(); err != nil {
		errs = append(errs, err)
	}

	if ce.ParentPolicy != nil {
		if err := ce.ParentPolicy.Validate(); err != nil {
			errs = append(errs, err)
		}
	}

	if err := ce.Event.Validate(); err != nil {
		errs = append(errs, err)
	}

	if err := ce.Policy.Validate(); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (ce *ComplianceEvent) Create(ctx context.Context, db *sql.DB) error {
	if ce.Event.ClusterID == 0 {
		ce.Event.ClusterID = ce.Cluster.KeyID
	}

	if ce.Event.PolicyID == 0 {
		ce.Event.PolicyID = ce.Policy.KeyID
	}

	if ce.Event.ParentPolicyID == nil && ce.ParentPolicy != nil {
		ce.Event.ParentPolicyID = &ce.ParentPolicy.KeyID
	}

	insertQuery, insertArgs := ce.Event.InsertQuery()

	row := db.QueryRowContext(ctx, insertQuery+"RETURNING id", insertArgs...) //nolint:execinquery
	if row.Err() != nil {
		return row.Err()
	}

	err := row.Scan(&ce.Event.KeyID)
	if err != nil {
		return err
	}

	return nil
}

type Cluster struct {
	KeyID     int    `db:"id" json:"-" dbOptions:"primaryKey"`
	Name      string `db:"name" json:"name" dbOptions:"inUniqueConstraint"`
	ClusterID string `db:"cluster_id" json:"cluster_id" dbOptions:"inUniqueConstraint"` //nolint:tagliatelle
}

func (c Cluster) Validate() error {
	errs := make([]error, 0)

	if c.Name == "" {
		errs = append(errs, fmt.Errorf("%w: cluster.name", errRequiredFieldNotProvided))
	}

	if c.ClusterID == "" {
		errs = append(errs, fmt.Errorf("%w: cluster.cluster_id", errRequiredFieldNotProvided))
	}

	return errors.Join(errs...)
}

func (c Cluster) GetTableName() string {
	return clustersTableName
}

func (c *Cluster) FromRow(rows *sql.Rows) error {
	return fromRow(rows, c)
}

func (c *Cluster) InsertQuery() (string, []any) {
	fields := getFields(c)

	return c.InsertQueryFromFields(fields)
}

func (c *Cluster) InsertQueryFromFields(fields map[string]dbField) (string, []any) {
	return generateInsertQuery(c.GetTableName(), fields)
}

func (c *Cluster) GetOrCreate(ctx context.Context, db *sql.DB) error {
	return getOrCreate(ctx, db, c)
}

type EventDetails struct {
	KeyID          int       `db:"id" json:"-" dbOptions:"primaryKey"`
	ClusterID      int       `db:"cluster_id" json:"-"`
	PolicyID       int       `db:"policy_id" json:"-"`
	ParentPolicyID *int      `db:"parent_policy_id" json:"-"`
	Compliance     string    `db:"compliance" json:"compliance"`
	Message        string    `db:"message" json:"message"`
	Timestamp      time.Time `db:"timestamp" json:"timestamp"`
	Metadata       JSONMap   `db:"metadata" json:"metadata,omitempty"`
	ReportedBy     *string   `db:"reported_by" json:"reported_by,omitempty"` //nolint:tagliatelle
}

func (e EventDetails) Validate() error {
	errs := make([]error, 0)

	if e.Compliance == "" {
		errs = append(errs, fmt.Errorf("%w: event.compliance", errRequiredFieldNotProvided))
	} else {
		switch e.Compliance {
		case "Compliant", "NonCompliant":
		default:
			errs = append(errs, fmt.Errorf("%w: event.compliance should be Compliant or NonCompliant, got %v",
				errInvalidInput, e.Compliance))
		}
	}

	if e.Message == "" {
		errs = append(errs, fmt.Errorf("%w: event.message", errRequiredFieldNotProvided))
	}

	if e.Timestamp.IsZero() {
		errs = append(errs, fmt.Errorf("%w: event.timestamp", errRequiredFieldNotProvided))
	}

	return errors.Join(errs...)
}

func (e EventDetails) GetTableName() string {
	return complianceEventsTableName
}

func (e *EventDetails) FromRow(rows *sql.Rows) error {
	return fromRow(rows, e)
}

func (e *EventDetails) InsertQuery() (string, []any) {
	fields := getFields(e)

	return e.InsertQueryFromFields(fields)
}

func (e *EventDetails) InsertQueryFromFields(fields map[string]dbField) (string, []any) {
	return generateInsertQuery(e.GetTableName(), fields)
}

type ParentPolicy struct {
	KeyID      int            `db:"id" json:"-" dbOptions:"primaryKey"`
	Name       string         `db:"name" json:"name" dbOptions:"inUniqueConstraint"`
	Namespace  string         `db:"namespace" json:"namespace" dbOptions:"inUniqueConstraint"`
	Categories pq.StringArray `db:"categories" json:"categories,omitempty" dbOptions:"inUniqueConstraint"`
	Controls   pq.StringArray `db:"controls" json:"controls,omitempty" dbOptions:"inUniqueConstraint"`
	Standards  pq.StringArray `db:"standards" json:"standards,omitempty" dbOptions:"inUniqueConstraint"`
}

func (p ParentPolicy) Validate() error {
	errs := []error{}

	if p.Name == "" {
		errs = append(errs, fmt.Errorf("%w: parent_policy.name", errRequiredFieldNotProvided))
	}

	if p.Namespace == "" {
		errs = append(errs, fmt.Errorf("%w: parent_policy.namespace", errRequiredFieldNotProvided))
	}

	return errors.Join(errs...)
}

func (p ParentPolicy) GetTableName() string {
	return parentPolicyTableName
}

func (p *ParentPolicy) FromRow(rows *sql.Rows) error {
	return fromRow(rows, p)
}

func (p *ParentPolicy) InsertQuery() (string, []any) {
	fields := getFields(p)

	return p.InsertQueryFromFields(fields)
}

func (p *ParentPolicy) InsertQueryFromFields(fields map[string]dbField) (string, []any) {
	return generateInsertQuery(p.GetTableName(), fields)
}

func (p *ParentPolicy) GetOrCreate(ctx context.Context, db *sql.DB) error {
	return getOrCreate(ctx, db, p)
}

func (p ParentPolicy) key() string {
	return fmt.Sprintf("%s;%v;%v;%v", p.Name, p.Categories, p.Controls, p.Standards)
}

type Policy struct {
	KeyID     int     `db:"id" json:"-" dbOptions:"primaryKey"`
	Kind      string  `db:"kind" json:"kind" dbOptions:"inUniqueConstraint"`
	APIGroup  string  `db:"api_group" json:"apiGroup" dbOptions:"inUniqueConstraint"`
	Name      string  `db:"name" json:"name" dbOptions:"inUniqueConstraint"`
	Namespace *string `db:"namespace" json:"namespace,omitempty" dbOptions:"inUniqueConstraint"`
	Spec      string  `db:"spec" json:"spec,omitempty"`
	SpecHash  string  `db:"spec_hash" json:"specHash" dbOptions:"inUniqueConstraint"`
	Severity  *string `db:"severity" json:"severity,omitempty" dbOptions:"inUniqueConstraint"`
}

func (p *Policy) Validate() error {
	errs := make([]error, 0)

	if p.APIGroup == "" {
		errs = append(errs, fmt.Errorf("%w: policy.apiGroup", errRequiredFieldNotProvided))
	}

	if p.Kind == "" {
		errs = append(errs, fmt.Errorf("%w: policy.kind", errRequiredFieldNotProvided))
	}

	if p.Name == "" {
		errs = append(errs, fmt.Errorf("%w: policy.name", errRequiredFieldNotProvided))
	}

	if p.Spec == "" && p.SpecHash == "" {
		errs = append(errs, fmt.Errorf("%w: policy.spec or policy.specHash", errRequiredFieldNotProvided))
	}

	if p.Spec != "" {
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(p.Spec)); err != nil {
			errs = append(errs, fmt.Errorf("%w: policy.spec is not valid JSON: %w", errInvalidInput, err))
		} else if buf.String() != p.Spec {
			errs = append(errs, fmt.Errorf("%w: policy.spec is not compact JSON", errInvalidInput))
		} else if p.SpecHash != "" {
			sum := sha1.Sum(buf.Bytes()) // #nosec G401 -- for convenience, not cryptography

			if p.SpecHash != hex.EncodeToString(sum[:]) {
				errs = append(errs, fmt.Errorf("%w: policy.specHash does not match the compact policy.Spec",
					errInvalidInput))
			}
		}
	}

	return errors.Join(errs...)
}

func (p Policy) GetTableName() string {
	return policyTableName
}

func (p *Policy) FromRow(rows *sql.Rows) error {
	return fromRow(rows, p)
}

func (p *Policy) InsertQuery() (string, []any) {
	fields := getFields(p)

	return p.InsertQueryFromFields(fields)
}

func (p *Policy) InsertQueryFromFields(fields map[string]dbField) (string, []any) {
	return generateInsertQuery(p.GetTableName(), fields)
}

func (p *Policy) GetOrCreate(ctx context.Context, db *sql.DB) error {
	return getOrCreate(ctx, db, p)
}

func (p *Policy) key() policyKey {
	key := policyKey{
		Kind:     p.Kind,
		APIGroup: p.APIGroup,
		Name:     p.Name,
	}

	if p.Namespace != nil {
		key.Namespace = *p.Namespace
	}

	if p.SpecHash != "" {
		key.SpecHash = p.SpecHash
	}

	if p.Severity != nil {
		key.Severity = *p.Severity
	}

	return key
}

type policyKey struct {
	Kind      string
	APIGroup  string
	Name      string
	Namespace string
	ParentID  string
	SpecHash  string
	Severity  string
}

type JSONMap map[string]interface{}

// Value returns a value that the database driver can use, or an error.
func (j JSONMap) Value() (driver.Value, error) {
	return json.Marshal(j)
}

// Scan allows for reading a JSONMap from the database.
func (j *JSONMap) Scan(src interface{}) error {
	var source []byte

	switch s := src.(type) {
	case string:
		source = []byte(s)
	case []byte:
		source = s
	case nil:
		source = nil
	default:
		return errors.New("incompatible type for JSONMap")
	}

	return json.Unmarshal(source, j)
}

type dbField struct {
	// value is the underlying value set on the struct field.
	value any
	// primaryKey is set when the struct field represents the primary key in the database row.
	primaryKey bool
	// inUniqueConstraint is set when this field is part of a unique constraint so that only unique columns are used in
	// SELECT queries.
	inUniqueConstraint bool
	// fieldIndex is the index used to access this struct field on the struct instance this field is a part of. This is
	// useful to set the value on the struct after a database query.
	fieldIndex int
	// goType is the Go type of this struct field. This is used to marhsall a SELECT query result to a struct.
	goType reflect.Type
}

// getFields parses the input object and returns a map where the keys are database column names and the values are
// dbField instances representing the struct fields on the input object.
func getFields(obj any) map[string]dbField {
	fields := map[string]dbField{}

	values := reflect.Indirect(reflect.ValueOf(obj))
	typesOf := values.Type()

	for i := 0; i < values.NumField(); i++ {
		structField := typesOf.Field(i)

		fieldName := structField.Tag.Get("db")
		if fieldName == "" {
			// Skip struct fields that are not database columns.
			continue
		}

		reflectField := values.Field(i)

		field := dbField{
			fieldIndex: i,
			goType:     structField.Type,
		}

		if structField.Tag.Get("dbOptions") != "" {
			dbOptions := strings.Split(structField.Tag.Get("dbOptions"), ";")

			if slices.Contains(dbOptions, "primaryKey") {
				field.primaryKey = true
			}

			if slices.Contains(dbOptions, "inUniqueConstraint") {
				field.inUniqueConstraint = true
			}
		}

		if reflectField.IsZero() {
			field.value = nil
		} else {
			field.value = reflect.Indirect(reflectField).Interface()
		}

		fields[fieldName] = field
	}

	return fields
}

// generateInsertQuery generates the SQL query and arguments for `db.Exec` based on the input `fields`. Note that this
// does not explicitly support nested structs.
func generateInsertQuery(tableName string, fields map[string]dbField) (string, []any) {
	fieldNames := make([]string, 0, len(fields))
	valueVariables := make([]string, 0, len(fields))

	for field := range fields {
		if fields[field].primaryKey {
			continue
		}

		fieldNames = append(fieldNames, field)
		valueVariables = append(valueVariables, fmt.Sprintf("$%d", len(fieldNames)))
	}

	sort.Strings(fieldNames)

	values := make([]any, 0, len(fields))

	for _, field := range fieldNames {
		values = append(values, fields[field].value)
	}

	insertQuery := fmt.Sprintf(
		`INSERT INTO "%s"(%s) VALUES(%s)`,
		tableName,
		strings.Join(fieldNames, ", "),
		strings.Join(valueVariables, ", "),
	)

	return insertQuery, values
}

// fromRow assigns the output from `rows.Scan` to the input `obj`. Note that this does not explicitly support
// nested structs.
func fromRow(rows *sql.Rows, obj any) error {
	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	objFields := getFields(obj)

	values := reflect.Indirect(reflect.ValueOf(obj))

	// scans is a slice of all the struct fields that will receive a value from the row
	scans := make([]any, 0, len(columns))

	for _, column := range columns {
		if _, ok := objFields[column]; !ok {
			continue
		}

		scans = append(scans, reflect.New(objFields[column].goType).Interface())
	}

	err = rows.Scan(scans...)
	if err != nil {
		return err
	}

	for i, column := range columns {
		if _, ok := objFields[column]; !ok {
			continue
		}

		field := values.Field(objFields[column].fieldIndex)
		src := reflect.Indirect(reflect.ValueOf(scans[i]))
		field.Set(src)
	}

	return nil
}

// getOrCreate will translate the input object to an INSERT SQL query. When the input object already exists in the
// database, a SELECT query is performed. The primary key is set on the input object when it is inserted or gotten
// from the database. The INSERT first then SELECT approach is a clean way to account for race conditions of multiple
// goroutines creating the same row.
func getOrCreate(ctx context.Context, db *sql.DB, obj dbRow) error {
	dbFields := getFields(obj)
	insertQuery, insertArgs := obj.InsertQueryFromFields(dbFields)

	var primaryKeyColumn string

	for field, fieldDetails := range dbFields {
		if fieldDetails.primaryKey {
			primaryKeyColumn = field

			break
		}
	}

	if primaryKeyColumn == "" {
		// This is a programming error which is why panic is used.
		panic("No primary key column is set")
	}

	// On inserts, it returns the primary key value (e.g. id). If it already exists, nothing is returned.
	row := db.QueryRowContext(
		ctx, fmt.Sprintf(`%s ON CONFLICT DO NOTHING RETURNING "%s"`, insertQuery, primaryKeyColumn), insertArgs...,
	)
	if row.Err() != nil {
		return row.Err()
	}

	var primaryKey int

	err := row.Scan(&primaryKey)
	if errors.Is(err, sql.ErrNoRows) {
		// The insertion did not return anything, so that means the value already exists, so perform a SELECT query
		// just using the unique columns. No more information is needed to get the right row and it allows the unique
		// indexes to be used to make the query more efficient.
		uniqueColumnValues := []any{}
		uniqueSelectQuery := []string{}

		for field, fieldDetails := range dbFields {
			if fieldDetails.inUniqueConstraint {
				uniqueColumnValues = append(uniqueColumnValues, fieldDetails.value)
				uniqueSelectQuery = append(uniqueSelectQuery, fmt.Sprintf("%s=$%d", field, len(uniqueSelectQuery)+1))
			}
		}

		if len(uniqueColumnValues) == 0 {
			// This is a programming error which is why panic is used.
			panic("No columns are part of a unique constraint")
		}

		row = db.QueryRowContext(
			ctx,
			fmt.Sprintf(
				`SELECT %s FROM %s WHERE %s`,
				primaryKeyColumn,
				obj.GetTableName(),
				strings.Join(uniqueSelectQuery, " AND "),
			),
			uniqueColumnValues...,
		)
		if row.Err() != nil {
			return row.Err()
		}

		err = row.Scan(&primaryKey)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Set the primary key value on the object
	values := reflect.Indirect(reflect.ValueOf(obj))
	field := values.Field(dbFields[primaryKeyColumn].fieldIndex)
	field.Set(reflect.ValueOf(primaryKey))

	return nil
}
