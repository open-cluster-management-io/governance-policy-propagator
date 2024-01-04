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
	"strings"
	"time"

	"github.com/lib/pq"
)

var (
	errRequiredFieldNotProvided = errors.New("required field not provided")
	errInvalidInput             = errors.New("invalid input")
)

type dbRow interface {
	InsertQuery() (string, []any)
	SelectQuery(returnedColumns ...string) (string, []any)
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

	row := db.QueryRowContext(ctx, insertQuery+" RETURNING id", insertArgs...) //nolint:execinquery
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
	KeyID     int    `db:"id" json:"-"`
	Name      string `db:"name" json:"name"`
	ClusterID string `db:"cluster_id" json:"cluster_id"` //nolint:tagliatelle
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

func (c *Cluster) InsertQuery() (string, []any) {
	sql := `INSERT INTO clusters (cluster_id, name) VALUES ($1, $2)`
	values := []any{c.ClusterID, c.Name}

	return sql, values
}

func (c *Cluster) SelectQuery(returnedColumns ...string) (string, []any) {
	if len(returnedColumns) == 0 {
		returnedColumns = []string{"*"}
	}

	sql := fmt.Sprintf(
		`SELECT %s FROM clusters WHERE cluster_id=$1 AND name=$2`,
		strings.Join(returnedColumns, ", "),
	)
	values := []any{c.ClusterID, c.Name}

	return sql, values
}

func (c *Cluster) GetOrCreate(ctx context.Context, db *sql.DB) error {
	return getOrCreate(ctx, db, c)
}

type EventDetails struct {
	KeyID          int       `db:"id" json:"-"`
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

func (e *EventDetails) InsertQuery() (string, []any) {
	sql := `INSERT INTO compliance_events` +
		`(cluster_id, compliance, message, metadata, parent_policy_id, policy_id, reported_by, timestamp) ` +
		`VALUES($1, $2, $3, $4, $5, $6, $7, $8)`
	values := []any{
		e.ClusterID, e.Compliance, e.Message, e.Metadata, e.ParentPolicyID, e.PolicyID, e.ReportedBy, e.Timestamp,
	}

	return sql, values
}

type ParentPolicy struct {
	KeyID      int            `db:"id" json:"-"`
	Name       string         `db:"name" json:"name"`
	Namespace  string         `db:"namespace" json:"namespace"`
	Categories pq.StringArray `db:"categories" json:"categories,omitempty"`
	Controls   pq.StringArray `db:"controls" json:"controls,omitempty"`
	Standards  pq.StringArray `db:"standards" json:"standards,omitempty"`
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

func (p *ParentPolicy) InsertQuery() (string, []any) {
	sql := `INSERT INTO parent_policies` +
		`(categories, controls, name, namespace, standards) ` +
		`VALUES($1, $2, $3, $4, $5)`
	values := []any{p.Categories, p.Controls, p.Name, p.Namespace, p.Standards}

	return sql, values
}

func (p *ParentPolicy) SelectQuery(returnedColumns ...string) (string, []any) {
	if len(returnedColumns) == 0 {
		returnedColumns = []string{"*"}
	}

	sql := fmt.Sprintf(
		`SELECT %s FROM parent_policies `+
			`WHERE categories=$1 AND controls=$2 AND name=$3 AND namespace=$4 AND standards=$5`,
		strings.Join(returnedColumns, ", "),
	)
	values := []any{p.Categories, p.Controls, p.Name, p.Namespace, p.Standards}

	return sql, values
}

func (p *ParentPolicy) GetOrCreate(ctx context.Context, db *sql.DB) error {
	return getOrCreate(ctx, db, p)
}

func (p ParentPolicy) key() string {
	return fmt.Sprintf("%s;%v;%v;%v", p.Name, p.Categories, p.Controls, p.Standards)
}

type Policy struct {
	KeyID     int     `db:"id" json:"-"`
	Kind      string  `db:"kind" json:"kind"`
	APIGroup  string  `db:"api_group" json:"apiGroup"`
	Name      string  `db:"name" json:"name"`
	Namespace *string `db:"namespace" json:"namespace,omitempty"`
	Spec      string  `db:"spec" json:"spec,omitempty"`
	SpecHash  string  `db:"spec_hash" json:"specHash"`
	Severity  *string `db:"severity" json:"severity,omitempty"`
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

func (p *Policy) InsertQuery() (string, []any) {
	sql := `INSERT INTO policies` +
		`(api_group, kind, name, namespace, severity, spec, spec_hash)` +
		`VALUES($1, $2, $3, $4, $5, $6, $7)`
	values := []any{p.APIGroup, p.Kind, p.Name, p.Namespace, p.Severity, p.Spec, p.SpecHash}

	return sql, values
}

func (p *Policy) SelectQuery(returnedColumns ...string) (string, []any) {
	if len(returnedColumns) == 0 {
		returnedColumns = []string{"*"}
	}

	sql := fmt.Sprintf(
		`SELECT %s FROM policies `+
			`WHERE api_group=$1 AND kind=$2 AND name=$3 AND namespace=$4 AND severity=$5 AND spec_hash=$6`,
		strings.Join(returnedColumns, ", "),
	)
	values := []any{p.APIGroup, p.Kind, p.Name, p.Namespace, p.Severity, p.SpecHash}

	return sql, values
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

// getOrCreate will translate the input object to an INSERT SQL query. When the input object already exists in the
// database, a SELECT query is performed. The primary key is set on the input object when it is inserted or gotten
// from the database. The INSERT first then SELECT approach is a clean way to account for race conditions of multiple
// goroutines creating the same row.
func getOrCreate(ctx context.Context, db *sql.DB, obj dbRow) error {
	insertQuery, insertArgs := obj.InsertQuery()

	// On inserts, it returns the primary key value (e.g. id). If it already exists, nothing is returned.
	row := db.QueryRowContext(ctx, fmt.Sprintf(`%s ON CONFLICT DO NOTHING RETURNING "id"`, insertQuery), insertArgs...)
	if row.Err() != nil {
		return row.Err()
	}

	var primaryKey int

	err := row.Scan(&primaryKey)
	if errors.Is(err, sql.ErrNoRows) {
		// The insertion did not return anything, so that means the value already exists, so perform a SELECT query
		// just using the unique columns. No more information is needed to get the right row and it allows the unique
		// indexes to be used to make the query more efficient.
		selectQuery, selectArgs := obj.SelectQuery("id")

		row = db.QueryRowContext(ctx, selectQuery, selectArgs...)
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
	values.FieldByName("KeyID").Set(reflect.ValueOf(primaryKey))

	return nil
}
