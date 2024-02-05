package complianceeventsapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/lib/pq"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

var (
	errRequiredFieldNotProvided = errors.New("required field not provided")
	errInvalidInput             = errors.New("invalid input")
	errDuplicateComplianceEvent = errors.New("the compliance event already exists")
)

type dbRow interface {
	InsertQuery() (string, []any)
	SelectQuery(returnedColumns ...string) (string, []any)
}

type metadata struct {
	Page    uint64 `json:"page"`
	Pages   uint64 `json:"pages"`
	PerPage uint64 `json:"per_page"` //nolint:tagliatelle
	Total   uint64 `json:"total"`
}

type ListResponse struct {
	Data     []ComplianceEvent `json:"data"`
	Metadata metadata          `json:"metadata"`
}

type queryOptions struct {
	ArrayFilters    map[string][]string
	Direction       string
	Filters         map[string][]string
	IncludeSpec     bool
	MessageIncludes string
	MessageLike     string
	NullFilters     []string
	Page            uint64
	PerPage         uint64
	Sort            []string
	TimestampAfter  time.Time
	TimestampBefore time.Time
}

type ComplianceEvent struct {
	EventID      int32         `json:"id"`
	Cluster      Cluster       `json:"cluster"`
	Event        EventDetails  `json:"event"`
	ParentPolicy *ParentPolicy `json:"parent_policy"` //nolint:tagliatelle
	Policy       Policy        `json:"policy"`
}

// Validate ensures that a valid POST request for a compliance event is set. This means that if the shorthand approach
// of providing parent_policy.id and/or policy.id is used, the other fields for ParentPolicy and Policy will not be
// present.
func (ce ComplianceEvent) Validate(ctx context.Context, db *sql.DB) error {
	errs := make([]error, 0)

	if err := ce.Cluster.Validate(); err != nil {
		errs = append(errs, err)
	}

	if ce.ParentPolicy != nil {
		if ce.ParentPolicy.KeyID != 0 {
			row := db.QueryRowContext(
				ctx, `SELECT EXISTS(SELECT * FROM parent_policies WHERE id=$1);`, ce.ParentPolicy.KeyID,
			)

			var exists bool

			err := row.Scan(&exists)
			if err != nil {
				log.Error(err, "Failed to query for the existence of the parent policy ID")

				return errors.New("failed to determine if parent_policy.id is valid")
			}

			if exists {
				// If the user provided extra data, ignore it since it won't be validated that it matches the database
				ce.ParentPolicy = &ParentPolicy{KeyID: ce.ParentPolicy.KeyID}
			} else {
				errs = append(errs, fmt.Errorf("%w: parent_policy.id not found", errInvalidInput))
			}
		} else if err := ce.ParentPolicy.Validate(); err != nil {
			errs = append(errs, err)
		}
	}

	if err := ce.Event.Validate(); err != nil {
		errs = append(errs, err)
	}

	if ce.Policy.KeyID != 0 {
		row := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT * FROM policies WHERE id=$1);`, ce.Policy.KeyID)

		var exists bool

		err := row.Scan(&exists)
		if err != nil {
			log.Error(err, "Failed to query for the existence of the policy ID")

			return errors.New("failed to determine if policy.id is valid")
		}

		if exists {
			// If the user provided extra data, ignore it since it won't be validated that it matches the database
			ce.Policy = Policy{KeyID: ce.Policy.KeyID}
		} else {
			errs = append(errs, fmt.Errorf("%w: policy.id not found", errInvalidInput))
		}
	} else if err := ce.Policy.Validate(); err != nil {
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

	row := db.QueryRowContext( //nolint:execinquery
		ctx, insertQuery+" ON CONFLICT DO NOTHING RETURNING id", insertArgs...,
	)

	err := row.Scan(&ce.Event.KeyID)
	if err != nil {
		// If this is true, then we know we encountered a conflict. This is simpler than parsing the unique constraint
		// error.
		if errors.Is(err, sql.ErrNoRows) {
			return errDuplicateComplianceEvent
		}

		return err
	}

	return nil
}

type Cluster struct {
	KeyID     int32  `db:"id" json:"-"`
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
	KeyID          int32     `db:"id" json:"-"`
	ClusterID      int32     `db:"cluster_id" json:"-"`
	PolicyID       int32     `db:"policy_id" json:"-"`
	ParentPolicyID *int32    `db:"parent_policy_id" json:"-"`
	Compliance     string    `db:"compliance" json:"compliance"`
	Message        string    `db:"message" json:"message"`
	Timestamp      time.Time `db:"timestamp" json:"timestamp"`
	Metadata       JSONMap   `db:"metadata" json:"metadata"`
	ReportedBy     *string   `db:"reported_by" json:"reported_by"` //nolint:tagliatelle
}

func (e EventDetails) Validate() error {
	errs := make([]error, 0)

	if e.Compliance == "" {
		errs = append(errs, fmt.Errorf("%w: event.compliance", errRequiredFieldNotProvided))
	} else {
		switch e.Compliance {
		case "Compliant", "NonCompliant", "Disabled", "Pending":
		default:
			errs = append(
				errs,
				fmt.Errorf(
					"%w: event.compliance should be Compliant, NonCompliant, Disabled, or Pending got %v",
					errInvalidInput, e.Compliance,
				),
			)
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
	KeyID      int32          `db:"id" json:"id"`
	Name       string         `db:"name" json:"name"`
	Namespace  string         `db:"namespace" json:"namespace"`
	Categories pq.StringArray `db:"categories" json:"categories"`
	Controls   pq.StringArray `db:"controls" json:"controls"`
	Standards  pq.StringArray `db:"standards" json:"standards"`
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
		`SELECT %s FROM parent_policies WHERE name=$1 AND namespace=$2`,
		strings.Join(returnedColumns, ", "),
	)
	values := []any{p.Name, p.Namespace}

	columnCount := 2

	if p.Categories == nil {
		sql += " AND categories IS NULL"
	} else {
		columnCount++
		sql += fmt.Sprintf(" AND categories=$%d", columnCount)
		values = append(values, p.Categories)
	}

	if p.Controls == nil {
		sql += " AND controls IS NULL"
	} else {
		columnCount++
		sql += fmt.Sprintf(" AND controls=$%d", columnCount)
		values = append(values, p.Controls)
	}

	if p.Standards == nil {
		sql += " AND standards IS NULL"
	} else {
		columnCount++
		sql += fmt.Sprintf(" AND standards=$%d", columnCount)
		values = append(values, p.Standards)
	}

	return sql, values
}

func (p *ParentPolicy) GetOrCreate(ctx context.Context, db *sql.DB) error {
	return getOrCreate(ctx, db, p)
}

func (p ParentPolicy) Key() string {
	return fmt.Sprintf("%s;%s;%v;%v;%v", p.Namespace, p.Name, p.Categories, p.Controls, p.Standards)
}

func ParentPolicyFromPolicyObj(plc *policiesv1.Policy) ParentPolicy {
	annotations := plc.GetAnnotations()
	categories := []string{}

	for _, category := range strings.Split(annotations["policy.open-cluster-management.io/categories"], ",") {
		category = strings.TrimSpace(category)
		if category != "" {
			categories = append(categories, category)
		}
	}

	controls := []string{}

	for _, control := range strings.Split(annotations["policy.open-cluster-management.io/controls"], ",") {
		control = strings.TrimSpace(control)
		if control != "" {
			controls = append(controls, control)
		}
	}

	standards := []string{}

	for _, standard := range strings.Split(annotations["policy.open-cluster-management.io/standards"], ",") {
		standard = strings.TrimSpace(standard)
		if standard != "" {
			standards = append(standards, standard)
		}
	}

	return ParentPolicy{
		Name:       plc.Name,
		Namespace:  plc.Namespace,
		Categories: categories,
		Controls:   controls,
		Standards:  standards,
	}
}

func PolicyFromUnstructured(obj unstructured.Unstructured) *Policy {
	policy := &Policy{}

	policy.APIGroup = obj.GetAPIVersion()
	policy.Kind = obj.GetKind()
	ns := obj.GetNamespace()

	if ns != "" {
		policy.Namespace = &ns
	}

	policy.Name = obj.GetName()

	spec, ok, _ := unstructured.NestedStringMap(obj.Object, "spec")
	if ok {
		typedSpec := JSONMap{}

		for key, val := range spec {
			typedSpec[key] = val
		}

		policy.Spec = typedSpec
	}

	if severity, ok := spec["severity"]; ok {
		policy.Severity = &severity
	}

	return policy
}

type Policy struct {
	KeyID     int32   `db:"id" json:"id"`
	Kind      string  `db:"kind" json:"kind"`
	APIGroup  string  `db:"api_group" json:"apiGroup"`
	Name      string  `db:"name" json:"name"`
	Namespace *string `db:"namespace" json:"namespace"`
	Spec      JSONMap `db:"spec" json:"spec,omitempty"`
	Severity  *string `db:"severity" json:"severity"`
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

	if p.Spec == nil {
		errs = append(errs, fmt.Errorf("%w: policy.spec", errRequiredFieldNotProvided))
	}

	return errors.Join(errs...)
}

func (p *Policy) InsertQuery() (string, []any) {
	sql := `INSERT INTO policies` +
		`(api_group, kind, name, namespace, severity, spec)` +
		`VALUES($1, $2, $3, $4, $5, $6)`
	values := []any{p.APIGroup, p.Kind, p.Name, p.Namespace, p.Severity, p.Spec}

	return sql, values
}

func (p *Policy) SelectQuery(returnedColumns ...string) (string, []any) {
	if len(returnedColumns) == 0 {
		returnedColumns = []string{"*"}
	}

	sql := fmt.Sprintf(
		`SELECT %s FROM policies `+
			`WHERE api_group=$1 AND kind=$2 AND name=$3 AND spec=$4`,
		strings.Join(returnedColumns, ", "),
	)

	values := []any{p.APIGroup, p.Kind, p.Name, p.Spec}

	columnCount := 4

	if p.Namespace == nil {
		sql += " AND namespace is NULL"
	} else {
		columnCount++
		sql += fmt.Sprintf(" AND namespace=$%d", columnCount)
		values = append(values, p.Namespace)
	}

	if p.Severity == nil {
		sql += " AND severity is NULL"
	} else {
		columnCount++
		sql += fmt.Sprintf(" AND severity=$%d", columnCount)
		values = append(values, p.Severity)
	}

	return sql, values
}

func (p *Policy) GetOrCreate(ctx context.Context, db *sql.DB) error {
	return getOrCreate(ctx, db, p)
}

func (p *Policy) Key() string {
	var namespace string

	if p.Namespace != nil {
		namespace = *p.Namespace
	}

	var severity string

	if p.Severity != nil {
		severity = *p.Severity
	}

	// Note that as of Go 1.20, it sorts the keys in the underlying map of p.Spec, which is why this is deterministic.
	// https://github.com/golang/go/blob/97c8ff8d53759e7a82b1862403df1694f2b6e073/src/fmt/print.go#L816-L828
	return fmt.Sprintf("%s;%s;%s;%v;%v;%v", p.APIGroup, p.Kind, p.Name, namespace, severity, p.Spec)
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

	var primaryKey int32

	err := row.Scan(&primaryKey)
	if errors.Is(err, sql.ErrNoRows) {
		// The insertion did not return anything, so that means the value already exists, so perform a SELECT query
		// just using the unique columns. No more information is needed to get the right row and it allows the unique
		// indexes to be used to make the query more efficient.
		selectQuery, selectArgs := obj.SelectQuery("id")

		row = db.QueryRowContext(ctx, selectQuery, selectArgs...)

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
