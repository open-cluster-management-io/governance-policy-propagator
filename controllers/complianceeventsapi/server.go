package complianceeventsapi

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/postgres" // blank import the dialect driver
	_ "github.com/lib/pq"
)

var (
	clusterKeyCache      sync.Map
	parentPolicyKeyCache sync.Map
	policyKeyCache       sync.Map
)

type complianceAPIServer struct {
	Lock      *sync.Mutex
	server    *http.Server
	addr      string
	isRunning bool
}

// Start starts the http server. If it is already running, it has no effect.
func (s *complianceAPIServer) Start(dbURL string) error {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	if s.isRunning {
		return nil
	}

	mux := http.NewServeMux()

	s.server = &http.Server{
		Addr:    s.addr,
		Handler: mux,

		// need to investigate ideal values for these
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return err
	}

	goquDB := goqu.New("postgres", db)

	// register handlers here
	mux.HandleFunc("/api/v1/compliance-events", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			postComplianceEvent(goquDB, w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	go func() {
		err := s.server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Error(err, "Error starting compliance events api server")
		}
	}()

	s.isRunning = true

	return nil
}

// Stop stops the http server. If it is not currently running, it has no effect.
func (s *complianceAPIServer) Stop() error {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	if !s.isRunning {
		return nil
	}

	if err := s.server.Shutdown(context.TODO()); err != nil {
		log.Error(err, "Error stopping compliance events api server")

		return err
	}

	s.isRunning = false

	return nil
}

func postComplianceEvent(goquDB *goqu.Database, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error(err, "error reading request body")
		writeErrMsgJSON(w, "Could not read request body", http.StatusBadRequest)

		return
	}

	reqEvent := &ComplianceEvent{}

	if err := json.Unmarshal(body, reqEvent); err != nil {
		writeErrMsgJSON(w, "Incorrectly formatted request body, must be valid JSON", http.StatusBadRequest)

		return
	}

	if err := reqEvent.Validate(); err != nil {
		writeErrMsgJSON(w, err.Error(), http.StatusBadRequest)

		return
	}

	clusterFK, err := getClusterForeignKey(r.Context(), goquDB, reqEvent.Cluster)
	if err != nil {
		log.Error(err, "error getting cluster foreign key")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	reqEvent.Event.ClusterID = clusterFK

	if reqEvent.ParentPolicy != nil {
		pfk, err := getParentPolicyForeignKey(r.Context(), goquDB, *reqEvent.ParentPolicy)
		if err != nil {
			log.Error(err, "error getting parent policy foreign key")
			writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

			return
		}

		reqEvent.Policy.ParentPolicyID = &pfk
	}

	policyFK, err := getPolicyForeignKey(r.Context(), goquDB, reqEvent.Policy)
	if err != nil {
		if errors.Is(err, errRequiredFieldNotProvided) {
			writeErrMsgJSON(w, err.Error(), http.StatusBadRequest)

			return
		}

		log.Error(err, "error getting policy foreign key")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	reqEvent.Event.PolicyID = policyFK

	insert := goquDB.Insert("compliance_events").Rows(reqEvent.Event).Executor()

	_, err = insert.Exec()
	if err != nil {
		log.Error(err, "error inserting compliance event")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	// remove the spec to only respond with the specHash
	reqEvent.Policy.Spec = nil

	resp, err := json.Marshal(reqEvent)
	if err != nil {
		log.Error(err, "error marshaling reqEvent for the response")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusCreated)

	if _, err = w.Write(resp); err != nil {
		log.Error(err, "error writing success response")
	}
}

func getClusterForeignKey(ctx context.Context, goquDB *goqu.Database, cluster Cluster) (int, error) {
	// Check cache
	key, ok := clusterKeyCache.Load(cluster.ClusterID)
	if ok {
		return key.(int), nil
	}

	foundCluster := new(Cluster)

	found, err := goquDB.From("clusters").
		Where(goqu.Ex{"cluster_id": cluster.ClusterID}).
		ScanStructContext(ctx, foundCluster)
	if err != nil {
		return 0, err
	}

	// If the row already exists
	if found {
		clusterKeyCache.Store(cluster.ClusterID, foundCluster.KeyID)

		return foundCluster.KeyID, nil
	}

	// Otherwise, create a new row in the table
	insert := goquDB.Insert("clusters").Returning("id").Rows(cluster).Executor()

	id := new(int)
	if _, err := insert.ScanValContext(ctx, id); err != nil {
		return 0, err
	}

	clusterKeyCache.Store(cluster.ClusterID, *id)

	return *id, nil
}

func getParentPolicyForeignKey(ctx context.Context, goquDB *goqu.Database, parent ParentPolicy) (int, error) {
	// Check cache
	parKey := parent.key()

	key, ok := parentPolicyKeyCache.Load(parKey)
	if ok {
		return key.(int), nil
	}

	foundParent := new(ParentPolicy)

	qu := goquDB.From("parent_policies").Where(goqu.Ex{
		"name":       parent.Name,
		"categories": goqu.L("?::text[]", parent.Categories),
		"controls":   goqu.L("?::text[]", parent.Controls),
		"standards":  goqu.L("?::text[]", parent.Standards),
	})

	found, err := qu.ScanStructContext(ctx, foundParent)
	if err != nil {
		return 0, err
	}

	// If the row already exists
	if found {
		parentPolicyKeyCache.Store(parKey, foundParent.KeyID)

		return foundParent.KeyID, nil
	}

	// Otherwise, create a new row in the table
	insert := goquDB.Insert("parent_policies").Returning("id").Rows(parent).Executor()

	id := new(int)
	if _, err := insert.ScanValContext(ctx, id); err != nil {
		return 0, err
	}

	parentPolicyKeyCache.Store(parKey, *id)

	return *id, nil
}

func getPolicyForeignKey(ctx context.Context, goquDB *goqu.Database, pol Policy) (int, error) {
	// Fill in missing fields that can be inferred from other fields
	if pol.SpecHash == nil {
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(*pol.Spec)); err != nil {
			return 0, err // This kind of error would have been found during validation
		}

		sum := sha1.Sum(buf.Bytes())
		hash := hex.EncodeToString(sum[:])
		pol.SpecHash = &hash
	}

	// Check cache
	polKey := pol.key()

	key, ok := policyKeyCache.Load(polKey)
	if ok {
		return key.(int), nil
	}

	foundPolicy := new(Policy)

	qu := goquDB.From("policies").Where(goqu.Ex{
		"kind":             pol.Kind,
		"api_group":        pol.APIGroup,
		"name":             pol.Name,
		"spec_hash":        pol.SpecHash,
		"namespace":        pol.Namespace,
		"parent_policy_id": pol.ParentPolicyID,
		"severity":         pol.Severity,
	})

	found, err := qu.ScanStructContext(ctx, foundPolicy)
	if err != nil {
		return 0, err
	}

	// If the row already exists
	if found {
		policyKeyCache.Store(polKey, foundPolicy.KeyID)

		return foundPolicy.KeyID, nil
	}

	// When creating a new row, `spec` is required
	if pol.Spec == nil {
		return 0, fmt.Errorf("%w: policy.spec is not optional for new policies", errRequiredFieldNotProvided)
	}

	// Otherwise, create a new row in the table
	insert := goquDB.Insert("policies").Returning("id").Rows(pol).Executor()

	id := new(int)
	if _, err := insert.ScanValContext(ctx, id); err != nil {
		return 0, err
	}

	policyKeyCache.Store(polKey, *id)

	return *id, nil
}

type errorMessage struct {
	Message string `json:"message"`
}

// writeErrMsgJSON wraps the given message in JSON like `{"message": <>}` and
// writes the response, setting the header to the given code. Since this message
// will be read by the user, take care not to leak any sensitive details that
// might be in the error message.
func writeErrMsgJSON(w http.ResponseWriter, message string, code int) {
	msg := errorMessage{Message: message}

	resp, err := json.Marshal(msg)
	if err != nil {
		log.Error(err, "error marshaling error message", "message", message)
	}

	w.WriteHeader(code)

	if _, err := w.Write(resp); err != nil {
		log.Error(err, "error writing error message")
	}
}
