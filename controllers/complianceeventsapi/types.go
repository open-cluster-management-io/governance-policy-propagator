package complianceeventsapi

import (
	"bytes"
	"crypto/sha1"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lib/pq"
)

var (
	errRequiredFieldNotProvided = errors.New("required field not provided")
	errInvalidInput             = errors.New("invalid input")
)

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

type Cluster struct {
	KeyID     int    `db:"id" json:"-" goqu:"skipinsert"`
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

type EventDetails struct {
	KeyID      int       `db:"id" json:"-" goqu:"skipinsert"`
	ClusterID  int       `db:"cluster_id" json:"-"`
	PolicyID   int       `db:"policy_id" json:"-"`
	Compliance string    `db:"compliance" json:"compliance"`
	Message    string    `db:"message" json:"message"`
	Timestamp  time.Time `db:"timestamp" json:"timestamp"`
	Metadata   JSONMap   `db:"metadata" json:"metadata,omitempty"`
	ReportedBy *string   `db:"reported_by" json:"reported_by,omitempty"` //nolint:tagliatelle
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

type ParentPolicy struct {
	KeyID      int            `db:"id" json:"-" goqu:"skipinsert"`
	Name       string         `db:"name" json:"name"`
	Categories pq.StringArray `db:"categories" json:"categories,omitempty"`
	Controls   pq.StringArray `db:"controls" json:"controls,omitempty"`
	Standards  pq.StringArray `db:"standards" json:"standards,omitempty"`
}

func (p ParentPolicy) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("%w: parent_policy.name", errRequiredFieldNotProvided)
	}

	return nil
}

func (p ParentPolicy) key() string {
	return fmt.Sprintf("%s;%v;%v;%v", p.Name, p.Categories, p.Controls, p.Standards)
}

type Policy struct {
	KeyID          int     `db:"id" json:"-" goqu:"skipinsert"`
	Kind           string  `db:"kind" json:"kind"`
	APIGroup       string  `db:"api_group" json:"apiGroup"`
	Name           string  `db:"name" json:"name"`
	Namespace      *string `db:"namespace" json:"namespace,omitempty"`
	ParentPolicyID *int    `db:"parent_policy_id" json:"-"`
	Spec           *string `db:"spec" json:"spec,omitempty"`
	SpecHash       *string `db:"spec_hash" json:"specHash,omitempty"`
	Severity       *string `db:"severity" json:"severity,omitempty"`
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

	if p.Spec == nil && p.SpecHash == nil {
		errs = append(errs, fmt.Errorf("%w: policy.spec or policy.specHash", errRequiredFieldNotProvided))
	}

	if p.Spec != nil {
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(*p.Spec)); err != nil {
			errs = append(errs, fmt.Errorf("%w: policy.spec is not valid JSON: %w", errInvalidInput, err))
		} else if buf.String() != *p.Spec {
			errs = append(errs, fmt.Errorf("%w: policy.spec is not compact JSON", errInvalidInput))
		} else if p.SpecHash != nil {
			sum := sha1.Sum(buf.Bytes())

			if *p.SpecHash != hex.EncodeToString(sum[:]) {
				errs = append(errs, fmt.Errorf("%w: policy.specHash does not match the compact policy.Spec",
					errInvalidInput))
			}
		}
	}

	return errors.Join(errs...)
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

	if p.ParentPolicyID != nil {
		key.ParentID = strconv.Itoa(*p.ParentPolicyID)
	}

	if p.SpecHash != nil {
		key.SpecHash = *p.SpecHash
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
