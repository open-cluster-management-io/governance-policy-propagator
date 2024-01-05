package complianceeventsapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// init dynamically parses the database columns of each struct type to create a mapping of user provided sort options
// to the equivalent SQL column. ErrInvalidSortOption is also defined with the available sort options to choose from.
func init() {
	tableToStruct := map[string]any{
		"clusters":          Cluster{},
		"compliance_events": EventDetails{},
		"parent_policies":   ParentPolicy{},
		"policies":          Policy{},
	}

	tableNameToJSONName := map[string]string{
		"clusters":          "cluster",
		"compliance_events": "event",
		"parent_policies":   "parent_policy",
		"policies":          "policy",
	}

	// ID is a special case since it's displayed at the top-level in the JSON but is actually in the compliance_events
	// table.
	sortOptionsToSQL = map[string]string{"id": "compliance_events.id"}
	sortOptionsKeys := []string{"id"}

	for tableName, tableStruct := range tableToStruct {
		structType := reflect.TypeOf(tableStruct)
		for i := 0; i < structType.NumField(); i++ {
			structField := structType.Field(i)

			jsonField := structField.Tag.Get("json")
			if jsonField == "" || jsonField == "-" {
				continue
			}

			// This removes additional text in tag such as capturing `spec` from `json:"spec,omitempty"`.
			jsonField = strings.SplitN(jsonField, ",", 2)[0]

			dbColumn := structField.Tag.Get("db")
			if dbColumn == "" {
				continue
			}

			// Skip JSONB columns as sortable options
			if tableName == "policies" && dbColumn == "spec" {
				continue
			}

			if tableName == "compliance_events" && dbColumn == "metadata" {
				continue
			}

			sortOption := fmt.Sprintf("%s.%s", tableNameToJSONName[tableName], jsonField)

			sortOptionsKeys = append(sortOptionsKeys, sortOption)

			sortOptionsToSQL[sortOption] = fmt.Sprintf("%s.%s", tableName, dbColumn)
		}
	}

	sort.Strings(sortOptionsKeys)

	ErrInvalidSortOption = fmt.Errorf(
		"an invalid sort option was provided, choose from: %s", strings.Join(sortOptionsKeys, ", "),
	)
}

var (
	clusterKeyCache      sync.Map
	parentPolicyKeyCache sync.Map
	policyKeyCache       sync.Map
	sortOptionsToSQL     map[string]string
	ErrInvalidSortOption error
	ErrInvalidQueryArg   = errors.New("invalid query argument")
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

	// register handlers here
	mux.HandleFunc("/api/v1/compliance-events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			getComplianceEvents(db, w, r)
		case http.MethodPost:
			postComplianceEvent(db, w, r)
		default:
			writeErrMsgJSON(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/compliance-events/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			writeErrMsgJSON(w, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		getSingleComplianceEvent(db, w, r)
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

// splitQueryValue will parse a string and split on unescaped commas. Empty values are discarded.
func splitQueryValue(value string) []string {
	values := []string{}

	var currentVal string
	var previousChar rune

	for _, char := range value {
		if char == ',' {
			if previousChar == '\\' {
				// This comma was escaped, so remove the escape character and keep the comma. Runes are used in case
				// unicode characters are present in currentVal.
				runeCurrentVal := []rune(currentVal)
				currentVal = string(runeCurrentVal[:len(runeCurrentVal)-1]) + ","
			} else {
				// The comma was not escaped so we encountered a new value.
				if currentVal != "" {
					values = append(values, currentVal)
				}

				currentVal = ""
			}
		} else {
			// A non-special character was encountered so just append the character.
			currentVal += string(char)
		}

		previousChar = char
	}

	if currentVal != "" {
		values = append(values, currentVal)
	}

	return values
}

// parseQueryArgs will parse the HTTP request's query arguments and convert them to a usable format for constructing
// the SQL query. All defaults are set and any invalid query arguments result in an error being returned.
func parseQueryArgs(queryArgs url.Values) (*queryOptions, error) {
	parsed := &queryOptions{}

	perPageStr := queryArgs.Get("per_page")

	if perPageStr == "" {
		parsed.PerPage = 20
	} else {
		var err error

		parsed.PerPage, err = strconv.ParseUint(perPageStr, 10, 64)
		if err != nil || parsed.PerPage == 0 || parsed.PerPage > 100 {
			return nil, fmt.Errorf("%w: per_page must be a value between 1 and 100", ErrInvalidQueryArg)
		}
	}

	pageStr := queryArgs.Get("page")

	if pageStr == "" {
		parsed.Page = 1
	} else {
		var err error

		parsed.Page, err = strconv.ParseUint(pageStr, 10, 64)
		if err != nil || parsed.Page == 0 {
			return nil, fmt.Errorf("%w: page must be a positive integer", ErrInvalidQueryArg)
		}
	}

	sortArgs := splitQueryValue(queryArgs.Get("sort"))

	if len(sortArgs) == 0 {
		parsed.Sort = []string{"compliance_events.timestamp"}
	} else {
		sortSQL := []string{}

		for _, sortArg := range sortArgs {
			sortOption, ok := sortOptionsToSQL[sortArg]
			if !ok {
				return nil, ErrInvalidSortOption
			}

			sortSQL = append(sortSQL, sortOption)
		}

		parsed.Sort = sortSQL
	}

	directionArg := queryArgs.Get("direction")
	if directionArg == "" || directionArg == "desc" {
		parsed.Direction = "DESC"
	} else if directionArg == "asc" {
		parsed.Direction = "ASC"
	} else {
		return nil, fmt.Errorf("%w: direction must be one of: asc, desc", ErrInvalidQueryArg)
	}

	if queryArgs.Get("include_spec") != "" {
		return nil, fmt.Errorf("%w: include_spec is a flag and does not accept a value", ErrInvalidQueryArg)
	}

	parsed.IncludeSpec = queryArgs.Has("include_spec")

	return parsed, nil
}

// generateGetComplianceEventsQuery will return a SELECT query with results ready to be parsed by
// scanIntoComplianceEvent. The caller is responsible for adding filters to the query.
func generateGetComplianceEventsQuery(includeSpec bool) string {
	selectArgs := []string{
		"compliance_events.id",
		"compliance_events.compliance",
		"compliance_events.message",
		"compliance_events.metadata",
		"compliance_events.reported_by",
		"compliance_events.timestamp",
		"clusters.cluster_id",
		"clusters.name",
		"parent_policies.id",
		"parent_policies.name",
		"parent_policies.namespace",
		"parent_policies.categories",
		"parent_policies.controls",
		"parent_policies.standards",
		"policies.id",
		"policies.api_group",
		"policies.kind",
		"policies.name",
		"policies.namespace",
		"policies.severity",
	}

	if includeSpec {
		selectArgs = append(selectArgs, "policies.spec")
	}

	return fmt.Sprintf(`SELECT %s
FROM
  compliance_events
  LEFT JOIN clusters ON compliance_events.cluster_id = clusters.id
  LEFT JOIN parent_policies ON compliance_events.parent_policy_id = parent_policies.id
  LEFT JOIN policies ON compliance_events.policy_id = policies.id`,
		strings.Join(selectArgs, ", "),
	)
}

type Scannable interface {
	Scan(dest ...any) error
}

// scanIntoComplianceEvent will scan the row result from the SELECT query generated by generateGetComplianceEventsQuery
// into a ComplianceEvent object.
func scanIntoComplianceEvent(rows Scannable, includeSpec bool) (*ComplianceEvent, error) {
	ce := ComplianceEvent{
		Cluster:      Cluster{},
		Event:        EventDetails{},
		ParentPolicy: nil,
		Policy:       Policy{},
	}

	ppID := sql.NullInt32{}
	ppName := sql.NullString{}
	ppNamespace := sql.NullString{}
	ppCategories := pq.StringArray{}
	ppControls := pq.StringArray{}
	ppStandards := pq.StringArray{}

	scanArgs := []any{
		&ce.EventID,
		&ce.Event.Compliance,
		&ce.Event.Message,
		&ce.Event.Metadata,
		&ce.Event.ReportedBy,
		&ce.Event.Timestamp,
		&ce.Cluster.ClusterID,
		&ce.Cluster.Name,
		&ppID,
		&ppName,
		&ppNamespace,
		&ppCategories,
		&ppControls,
		&ppStandards,
		&ce.Policy.KeyID,
		&ce.Policy.APIGroup,
		&ce.Policy.Kind,
		&ce.Policy.Name,
		&ce.Policy.Namespace,
		&ce.Policy.Severity,
	}

	if includeSpec {
		scanArgs = append(scanArgs, &ce.Policy.Spec)
	}

	err := rows.Scan(scanArgs...)
	if err != nil {
		return nil, err
	}

	// The parent policy is optional but when it's set, the name is guaranteed to be set.
	if ppName.String != "" {
		ce.ParentPolicy = &ParentPolicy{
			KeyID:      ppID.Int32,
			Name:       ppName.String,
			Namespace:  ppNamespace.String,
			Categories: ppCategories,
			Controls:   ppControls,
			Standards:  ppStandards,
		}
	}

	return &ce, nil
}

// getSingleComplianceEvent handles the GET API endpoint for a single compliance event by ID.
func getSingleComplianceEvent(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	eventIDStr := strings.TrimPrefix(r.URL.Path, "/api/v1/compliance-events/")

	eventID, err := strconv.ParseUint(eventIDStr, 10, 64)
	if err != nil {
		writeErrMsgJSON(w, "The provided compliance event ID is invalid", http.StatusBadRequest)

		return
	}

	query := fmt.Sprintf("%s\nWHERE compliance_events.id = $1;", generateGetComplianceEventsQuery(true))

	row := db.QueryRowContext(r.Context(), query, eventID)
	if row.Err() != nil {
		log.Error(err, "Failed to query for the compliance event", "eventID", eventID)
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	complianceEvent, err := scanIntoComplianceEvent(row, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErrMsgJSON(w, "The requested compliance event was not found", http.StatusNotFound)

			return
		}

		log.Error(err, "Failed to unmarshal the database results")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	jsonResp, err := json.Marshal(complianceEvent)
	if err != nil {
		log.Error(err, "Failed marshal the compliance event", "eventID", eventID)
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	if _, err = w.Write(jsonResp); err != nil {
		log.Error(err, "Error writing success response")
	}
}

// getComplianceEvents handles the list API endpoint for compliance events.
func getComplianceEvents(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	queryArgs, err := parseQueryArgs(r.URL.Query())
	if err != nil {
		writeErrMsgJSON(w, err.Error(), http.StatusBadRequest)

		return
	}

	query := fmt.Sprintf(`%s
ORDER BY %s %s
LIMIT %d
OFFSET %d ROWS;`,
		generateGetComplianceEventsQuery(queryArgs.IncludeSpec),
		strings.Join(queryArgs.Sort, ", "),
		queryArgs.Direction,
		queryArgs.PerPage,
		(queryArgs.Page-1)*queryArgs.PerPage,
	)

	rows, err := db.QueryContext(r.Context(), query)
	if err == nil {
		err = rows.Err()
	}

	defer rows.Close()

	if err != nil {
		log.Error(err, "Failed to query for compliance events")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	complianceEvents := make([]ComplianceEvent, 0, queryArgs.PerPage)

	for rows.Next() {
		ce, err := scanIntoComplianceEvent(rows, queryArgs.IncludeSpec)
		if err != nil {
			log.Error(err, "Failed to unmarshal the database results")
			writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

			return
		}

		complianceEvents = append(complianceEvents, *ce)
	}

	row := db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM compliance_events;`)
	if row.Err() != nil {
		log.Error(row.Err(), "Failed to get the count of compliance events")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	var total uint64

	if err := row.Scan(&total); err != nil {
		log.Error(row.Err(), "Got an invalid response when getting the count of compliance events")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	pages := math.Ceil(float64(total) / float64(queryArgs.PerPage))

	response := ListResponse{
		Data: complianceEvents,
		Metadata: metadata{
			Page:    queryArgs.Page,
			Pages:   uint64(pages),
			PerPage: queryArgs.PerPage,
			Total:   total,
		},
	}

	jsonResp, err := json.Marshal(response)
	if err != nil {
		log.Error(err, "Failed to marshal the response")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	if _, err = w.Write(jsonResp); err != nil {
		log.Error(err, "Error writing success response")
	}
}

func postComplianceEvent(db *sql.DB, w http.ResponseWriter, r *http.Request) {
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

	if err := reqEvent.Validate(r.Context(), db); err != nil {
		writeErrMsgJSON(w, err.Error(), http.StatusBadRequest)

		return
	}

	clusterFK, err := getClusterForeignKey(r.Context(), db, reqEvent.Cluster)
	if err != nil {
		log.Error(err, "error getting cluster foreign key")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	reqEvent.Event.ClusterID = clusterFK

	if reqEvent.ParentPolicy != nil {
		pfk, err := getParentPolicyForeignKey(r.Context(), db, *reqEvent.ParentPolicy)
		if err != nil {
			log.Error(err, "error getting parent policy foreign key")
			writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

			return
		}

		reqEvent.Event.ParentPolicyID = &pfk
	}

	policyFK, err := getPolicyForeignKey(r.Context(), db, reqEvent.Policy)
	if err != nil {
		log.Error(err, "error getting policy foreign key")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	reqEvent.Event.PolicyID = policyFK

	err = reqEvent.Create(r.Context(), db)
	if err != nil {
		log.Error(err, "error inserting compliance event")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	// remove the spec so it's not returned in the JSON.
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

func getClusterForeignKey(ctx context.Context, db *sql.DB, cluster Cluster) (int32, error) {
	// Check cache
	key, ok := clusterKeyCache.Load(cluster.ClusterID)
	if ok {
		return key.(int32), nil
	}

	err := cluster.GetOrCreate(ctx, db)
	if err != nil {
		return 0, err
	}

	clusterKeyCache.Store(cluster.ClusterID, cluster.KeyID)

	return cluster.KeyID, nil
}

func getParentPolicyForeignKey(ctx context.Context, db *sql.DB, parent ParentPolicy) (int32, error) {
	if parent.KeyID != 0 {
		return parent.KeyID, nil
	}

	// Check cache
	parKey := parent.key()

	key, ok := parentPolicyKeyCache.Load(parKey)
	if ok {
		return key.(int32), nil
	}

	err := parent.GetOrCreate(ctx, db)
	if err != nil {
		return 0, err
	}

	parentPolicyKeyCache.Store(parKey, parent.KeyID)

	return parent.KeyID, nil
}

func getPolicyForeignKey(ctx context.Context, db *sql.DB, pol Policy) (int32, error) {
	if pol.KeyID != 0 {
		return pol.KeyID, nil
	}

	// Check cache
	polKey := pol.key()

	key, ok := policyKeyCache.Load(polKey)
	if ok {
		return key.(int32), nil
	}

	err := pol.GetOrCreate(ctx, db)
	if err != nil {
		return 0, err
	}

	policyKeyCache.Store(polKey, pol.KeyID)

	return pol.KeyID, nil
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
