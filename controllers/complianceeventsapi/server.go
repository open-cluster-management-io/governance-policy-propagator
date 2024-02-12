package complianceeventsapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
	apiserverx509 "k8s.io/apiserver/pkg/authentication/request/x509"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// init dynamically parses the database columns of each struct type to create a mapping of user provided sort/filter
// options to the equivalent SQL column. ErrInvalidSortOption, ErrInvalidQueryArg, and validQueryArgs are also defined
// with the available sort/query options to choose from.
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
	queryOptionsToSQL = map[string]string{"id": "compliance_events.id"}
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

			queryOption := fmt.Sprintf("%s.%s", tableNameToJSONName[tableName], jsonField)

			sortOptionsKeys = append(sortOptionsKeys, queryOption)
			validQueryArgs = append(validQueryArgs, queryOption)

			queryOptionsToSQL[queryOption] = fmt.Sprintf("%s.%s", tableName, dbColumn)
		}
	}

	sort.Strings(sortOptionsKeys)

	ErrInvalidSortOption = fmt.Errorf(
		"an invalid sort option was provided, choose from: %s", strings.Join(sortOptionsKeys, ", "),
	)

	validQueryArgs = []string{
		"direction",
		"event.message_includes",
		"event.message_like",
		"event.timestamp_after",
		"event.timestamp_before",
		"include_spec",
		"page",
		"per_page",
		"sort",
	}

	validQueryArgs = append(
		validQueryArgs,
		// sortOptionsKeys are all filterable columns.
		sortOptionsKeys...,
	)

	sort.Strings(validQueryArgs)

	ErrInvalidQueryArg = fmt.Errorf(
		"an invalid query argument was provided, choose from: %s", strings.Join(validQueryArgs, ", "),
	)
}

var (
	clusterKeyCache         sync.Map
	parentPolicyKeyCache    sync.Map
	policyKeyCache          sync.Map
	queryOptionsToSQL       map[string]string
	validQueryArgs          []string
	ErrInvalidSortOption    error
	ErrInvalidQueryArgValue = errors.New("invalid query argument")
	ErrInvalidQueryArg      error
	ErrNotAuthorized        = errors.New("not authorized")
)

type ComplianceAPIServer struct {
	server       *http.Server
	addr         string
	clientAuthCA *x509.CertPool
	cert         *tls.Certificate
	cfg          *rest.Config
}

func NewComplianceAPIServer(
	listenAddress string, cfg *rest.Config, clientAuthCA *x509.CertPool, cert *tls.Certificate,
) *ComplianceAPIServer {
	return &ComplianceAPIServer{
		addr:         listenAddress,
		clientAuthCA: clientAuthCA,
		cert:         cert,
		cfg:          cfg,
	}
}

// Start starts the HTTP server and blocks until ctx is closed or there was an error starting the
// HTTP server.
func (s *ComplianceAPIServer) Start(ctx context.Context, serverContext *ComplianceServerCtx) error {
	mux := http.NewServeMux()

	s.server = &http.Server{
		Addr:    s.addr,
		Handler: mux,

		// need to investigate ideal values for these
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	var authenticator *apiserverx509.Authenticator
	var authenticatedClient *kubernetes.Clientset

	if s.cert != nil {
		s.server.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{*s.cert},
			// Let the Kubernetes apiserver package validate it if the certificate is presented
			ClientAuth: tls.RequestClientCert,
			ClientCAs:  s.clientAuthCA,
		}

		listener = tls.NewListener(listener, s.server.TLSConfig)

		var err error

		authenticator, err = getCertAuthenticator(s.cfg)
		if err != nil {
			return err
		}

		authenticatedClient, err = kubernetes.NewForConfig(s.cfg)
		if err != nil {
			return err
		}
	}

	// register handlers here
	mux.HandleFunc("/api/v1/compliance-events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		serverContext.Lock.RLock()
		defer serverContext.Lock.RUnlock()

		if serverContext.DB == nil || serverContext.DB.PingContext(r.Context()) != nil {
			writeErrMsgJSON(w, "The database is unavailable", http.StatusInternalServerError)

			return
		}

		switch r.Method {
		case http.MethodGet:
			getComplianceEvents(serverContext.DB, w, r)
		case http.MethodPost:
			postComplianceEvent(serverContext.DB, s.cfg, authenticatedClient, authenticator, w, r)
		default:
			writeErrMsgJSON(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/compliance-events/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		serverContext.Lock.RLock()
		defer serverContext.Lock.RUnlock()

		if serverContext.DB == nil || serverContext.DB.PingContext(r.Context()) != nil {
			writeErrMsgJSON(w, "The database is unavailable", http.StatusInternalServerError)

			return
		}

		if r.Method != http.MethodGet {
			writeErrMsgJSON(w, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		getSingleComplianceEvent(serverContext.DB, w, r)
	})

	serveErr := make(chan error)

	go func() {
		defer close(serveErr)

		err := s.server.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		err := s.server.Shutdown(context.Background())
		if err != nil {
			log.Error(err, "Failed to shutdown the compliance API server")
		}

		return nil
	case err, closed := <-serveErr:
		if err != nil {
			return err
		}

		if closed {
			return errors.New("the compliance API server unexpectedly shutdown without an error")
		}

		return nil
	}
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
	parsed := &queryOptions{
		Direction:    "desc",
		Page:         1,
		PerPage:      20,
		Sort:         []string{"compliance_events.timestamp"},
		ArrayFilters: map[string][]string{},
		Filters:      map[string][]string{},
		NullFilters:  []string{},
	}

	for arg := range queryArgs {
		valid := false

		for _, validQueryArg := range validQueryArgs {
			if arg == validQueryArg {
				valid = true

				break
			}
		}

		if !valid {
			return nil, ErrInvalidQueryArg
		}

		sqlName, hasSQLName := queryOptionsToSQL[arg]

		value := queryArgs.Get(arg)
		if value == "" && arg != "include_spec" {
			// Only support null filters if it's a SQL column
			if !hasSQLName {
				return nil, fmt.Errorf("%w: %s must have a value", ErrInvalidQueryArgValue, arg)
			}

			parsed.NullFilters = append(parsed.NullFilters, sqlName)

			continue
		}

		switch arg {
		case "direction":
			if value == "desc" {
				parsed.Direction = "DESC"
			} else if value == "asc" {
				parsed.Direction = "ASC"
			} else {
				return nil, fmt.Errorf("%w: direction must be one of: asc, desc", ErrInvalidQueryArg)
			}
		case "include_spec":
			if value != "" {
				return nil, fmt.Errorf("%w: include_spec is a flag and does not accept a value", ErrInvalidQueryArg)
			}

			parsed.IncludeSpec = true
		case "page":
			var err error

			parsed.Page, err = strconv.ParseUint(value, 10, 64)
			if err != nil || parsed.Page == 0 {
				return nil, fmt.Errorf("%w: page must be a positive integer", ErrInvalidQueryArg)
			}
		case "per_page":
			var err error

			parsed.PerPage, err = strconv.ParseUint(value, 10, 64)
			if err != nil || parsed.PerPage == 0 || parsed.PerPage > 100 {
				return nil, fmt.Errorf("%w: per_page must be a value between 1 and 100", ErrInvalidQueryArg)
			}
		case "sort":
			sortArgs := splitQueryValue(value)

			sortSQL := []string{}

			for _, sortArg := range sortArgs {
				sortOption, ok := queryOptionsToSQL[sortArg]
				if !ok {
					return nil, ErrInvalidSortOption
				}

				sortSQL = append(sortSQL, sortOption)
			}

			parsed.Sort = sortSQL
		case "parent_policy.categories", "parent_policy.controls", "parent_policy.standards":
			parsed.ArrayFilters[sqlName] = splitQueryValue(value)
		case "event.message_includes":
			// Escape the SQL LIKE operators because we aren't exposing that functionality.
			escapedVal := strings.ReplaceAll(value, "%", `\%`)
			escapedVal = strings.ReplaceAll(escapedVal, "_", `\_`)
			// Add wildcards at the beginning and end of the search keyword for substring matching.
			parsed.MessageIncludes = "%" + escapedVal + "%"
		case "event.message_like":
			parsed.MessageLike = value
		case "event.timestamp_before":
			var err error

			parsed.TimestampBefore, err = time.Parse(time.RFC3339, value)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: event.timestamp_before must be in the format of RFC 3339", ErrInvalidQueryArgValue,
				)
			}
		case "event.timestamp_after":
			var err error

			parsed.TimestampAfter, err = time.Parse(time.RFC3339, value)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: event.timestamp_after must be in the format of RFC 3339", ErrInvalidQueryArgValue,
				)
			}
		default:
			// Standard string filtering
			parsed.Filters[sqlName] = splitQueryValue(value)
		}
	}

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

// getWhereClause will convert the input queryOptions to a WHERE statement and return the filter values for a prepared
// statement.
func getWhereClause(options *queryOptions) (string, []any) {
	filterSQL := []string{}
	filterValues := []any{}

	for sqlColumn, values := range options.Filters {
		if len(values) == 0 {
			continue
		}

		for i, value := range values {
			filterValues = append(filterValues, value)
			// For example: compliance_events.name=$1
			filter := fmt.Sprintf("%s=$%d", sqlColumn, len(filterValues))
			if i == 0 {
				filterSQL = append(filterSQL, "("+filter)
			} else {
				filterSQL[len(filterSQL)-1] += " OR " + filter
			}
		}

		filterSQL[len(filterSQL)-1] += ")"
	}

	for sqlColumn, values := range options.ArrayFilters {
		if len(values) == 0 {
			continue
		}

		for i, value := range values {
			filterValues = append(filterValues, value)

			// For example: $1=ANY(parent_policies.categories)
			filter := fmt.Sprintf("$%d=ANY(%s)", len(filterValues), sqlColumn)
			if i == 0 {
				filterSQL = append(filterSQL, "("+filter)
			} else {
				filterSQL[len(filterSQL)-1] += " OR " + filter
			}
		}

		filterSQL[len(filterSQL)-1] += ")"
	}

	for _, sqlColumn := range options.NullFilters {
		filterSQL = append(filterSQL, fmt.Sprintf("%s IS NULL", sqlColumn))
	}

	if options.MessageIncludes != "" {
		filterValues = append(filterValues, options.MessageIncludes)

		filterSQL = append(filterSQL, fmt.Sprintf("compliance_events.message LIKE $%d", len(filterValues)))
	}

	if options.MessageLike != "" {
		filterValues = append(filterValues, options.MessageLike)

		filterSQL = append(filterSQL, fmt.Sprintf("compliance_events.message LIKE $%d", len(filterValues)))
	}

	if !options.TimestampAfter.IsZero() {
		filterValues = append(filterValues, options.TimestampAfter)

		filterSQL = append(filterSQL, fmt.Sprintf("compliance_events.timestamp > $%d", len(filterValues)))
	}

	if !options.TimestampBefore.IsZero() {
		filterValues = append(filterValues, options.TimestampBefore)

		filterSQL = append(filterSQL, fmt.Sprintf("compliance_events.timestamp < $%d", len(filterValues)))
	}

	var whereClause string

	if len(filterSQL) > 0 {
		// For example:
		// WHERE (policy.name=$1) AND ($2=ANY(parent_policies.categories) OR $3=ANY(parent_policies.categories))
		whereClause = "\nWHERE " + strings.Join(filterSQL, " AND ")
	}

	return whereClause, filterValues
}

// getComplianceEvents handles the list API endpoint for compliance events.
func getComplianceEvents(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	queryArgs, err := parseQueryArgs(r.URL.Query())
	if err != nil {
		writeErrMsgJSON(w, err.Error(), http.StatusBadRequest)

		return
	}

	// Note that the where clause could be an empty string if not filters were passed in the query arguments.
	whereClause, filterValues := getWhereClause(queryArgs)

	// Example query:
	//   SELECT compliance_events.id, compliance_events.compliance, ...
	//     FROM compliance_events
	//   LEFT JOIN clusters ON compliance_events.cluster_id = clusters.id
	//   LEFT JOIN parent_policies ON compliance_events.parent_policy_id = parent_policies.id
	//   LEFT JOIN policies ON compliance_events.policy_id = policies.id
	//   WHERE (policies.name=$1 OR policies.name=$2) AND (policies.kind=$3)
	//   ORDER BY compliance_events.timestamp desc
	//   LIMIT 20
	//   OFFSET 0 ROWS;
	query := fmt.Sprintf(`%s%s
ORDER BY %s %s
LIMIT %d
OFFSET %d ROWS;`,
		generateGetComplianceEventsQuery(queryArgs.IncludeSpec),
		whereClause,
		strings.Join(queryArgs.Sort, ", "),
		queryArgs.Direction,
		queryArgs.PerPage,
		(queryArgs.Page-1)*queryArgs.PerPage,
	)

	rows, err := db.QueryContext(r.Context(), query, filterValues...)
	if err == nil {
		err = rows.Err()
	}

	if err != nil {
		log.Error(err, "Failed to query for compliance events")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	defer rows.Close()

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

	countQuery := `SELECT COUNT(*) FROM compliance_events
LEFT JOIN clusters ON compliance_events.cluster_id = clusters.id
LEFT JOIN parent_policies ON compliance_events.parent_policy_id = parent_policies.id
LEFT JOIN policies ON compliance_events.policy_id = policies.id` + whereClause // #nosec G202

	row := db.QueryRowContext(r.Context(), countQuery, filterValues...)

	var total uint64

	if err := row.Scan(&total); err != nil {
		log.Error(err, "Failed to get the count of compliance events")
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

func postComplianceEvent(db *sql.DB,
	cfg *rest.Config,
	authenticatedClient *kubernetes.Clientset,
	authenticator *apiserverx509.Authenticator,
	w http.ResponseWriter,
	r *http.Request,
) {
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

	allowed, err := canRecordComplianceEvent(cfg, authenticatedClient, authenticator, reqEvent.Cluster.Name, r)
	if err != nil {
		if errors.Is(err, ErrNotAuthorized) {
			writeErrMsgJSON(w, "Unauthorized", http.StatusUnauthorized)

			return
		}

		log.Error(err, "error determining if the user is authorized for recording compliance events")
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	if !allowed {
		// Logging is handled by canRecordComplianceEvent
		writeErrMsgJSON(w, "Forbidden", http.StatusForbidden)

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
		if errors.Is(err, errDuplicateComplianceEvent) {
			writeErrMsgJSON(w, "The compliance event already exists", http.StatusConflict)

			return
		}

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
	parKey := parent.Key()

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
	polKey := pol.Key()

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
