package complianceeventsapi

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"math"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
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

const (
	postgresForeignKeyViolationCode = "23503"
	postgresUniqueViolationCode     = "23505"
)

var (
	clusterKeyCache         sync.Map
	queryOptionsToSQL       map[string]string
	validQueryArgs          []string
	ErrInvalidSortOption    error
	ErrInvalidQueryArgValue = errors.New("invalid query argument")
	ErrInvalidQueryArg      error
	ErrUnauthorized         = errors.New("not authorized")
	ErrForbidden            = errors.New("the request is not allowed")
	// The user has no access to any managed cluster
	ErrNoAccess = errors.New("the user has no access")
)

type ComplianceAPIServer struct {
	server *http.Server
	addr   string
	cert   *tls.Certificate
	cfg    *rest.Config
}

func NewComplianceAPIServer(listenAddress string, cfg *rest.Config, cert *tls.Certificate) *ComplianceAPIServer {
	return &ComplianceAPIServer{
		addr: listenAddress,
		cert: cert,
		cfg:  cfg,
	}
}

type serverErrorLogWriter struct{}

func (*serverErrorLogWriter) Write(p []byte) (int, error) {
	m := string(p)

	// The OpenShift router (haproxy) seems to perform TCP checks to see if the connection is available. When it does
	// this, it resets the connection when done, which causes a log message every 5 seconds, so this will filter it out.
	if strings.HasPrefix(m, "http: TLS handshake error") && strings.HasSuffix(m, ": connection reset by peer\n") {
		log.V(2).Info(m)
	} else {
		log.Info(m)
	}

	return len(p), nil
}

func newServerErrorLog() *stdlog.Logger {
	return stdlog.New(&serverErrorLogWriter{}, "", 0)
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
		ErrorLog:     newServerErrorLog(),
	}

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	if s.cert != nil {
		s.server.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{*s.cert},
		}

		listener = tls.NewListener(listener, s.server.TLSConfig)
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
			// To verify each request independently
			userConfig, err := getUserKubeConfig(s.cfg, r)
			if err != nil {
				if errors.Is(err, ErrUnauthorized) {
					writeErrMsgJSON(w, "The Authorization header is not set", http.StatusUnauthorized)
				}

				return
			}
			getComplianceEvents(serverContext.DB, w, r, userConfig)
		case http.MethodPost:
			postComplianceEvent(serverContext, s.cfg, w, r)
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

		// To verify each request independently
		userConfig, err := getUserKubeConfig(s.cfg, r)
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				writeErrMsgJSON(w, "The Authorization header is not set", http.StatusUnauthorized)
			}

			return
		}

		getSingleComplianceEvent(serverContext.DB, w, r, userConfig)
	})

	mux.HandleFunc("/api/v1/reports/compliance-events", func(w http.ResponseWriter, r *http.Request) {
		// This header is for error writings
		w.Header().Set("Content-Type", "application/json")

		// To verify each request independently
		userConfig, err := getUserKubeConfig(s.cfg, r)
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				writeErrMsgJSON(w, "The Authorization header is not set", http.StatusUnauthorized)
			}

			return
		}

		if r.Method != http.MethodGet {
			writeErrMsgJSON(w, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		getComplianceEventsCSV(serverContext.DB, w, r, userConfig)
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
func parseQueryArgs(ctx context.Context, queryArgs url.Values, db *sql.DB,
	userConfig *rest.Config, isCSV bool,
) (*queryOptions, error) {
	parsed := &queryOptions{
		Direction:    "desc",
		Page:         1,
		PerPage:      20,
		Sort:         []string{"compliance_events.timestamp"},
		ArrayFilters: map[string][]string{},
		Filters:      map[string][]string{},
		NullFilters:  []string{},
	}

	// Case return CSV file, default PerPage is 0. Unlimited
	if isCSV {
		parsed.PerPage = 0
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

	parsed, err := setAuthorizedClusters(ctx, db, parsed, userConfig)
	if err != nil {
		// ErrNoAccess needs queryOptions
		return parsed, err
	}

	return parsed, nil
}

// setAuthorizedClusters verifies that if a cluster filter is provided,
// the user has access to this filter. If no cluster filter is provided,
// it sets the cluster filter to all managed clusters the user has access to.
// If the user has no access, then ErrNoAccess is returned.
func setAuthorizedClusters(ctx context.Context, db *sql.DB, parsed *queryOptions,
	userConfig *rest.Config,
) (*queryOptions, error) {
	unAuthorizedClusters := []string{}

	// Get all managedCluster rules
	allRules, err := getManagedClusterRules(userConfig, nil)
	if err != nil {
		return parsed, err
	}

	if slices.Contains(allRules["*"], "get") || slices.Contains(allRules["*"], "*") {
		return parsed, nil
	}

	clusterIDs := parsed.Filters["clusters.cluster_id"]
	// Temporarily reset clusters.cluster_id and repopulate with all known cluster IDs
	parsed.Filters["clusters.cluster_id"] = []string{}

	// Convert id to name
	for _, id := range clusterIDs {
		clusterName, err := getClusterNameFromID(ctx, db, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Filter out invalid cluster IDs from the query
				continue
			}

			log.Error(err, "Failed to get cluster name from cluster ID", getPqErrKeyVals(err, "ID", id)...)

			return parsed, err
		}

		if !getAccessByClusterName(allRules, clusterName) {
			unAuthorizedClusters = append(unAuthorizedClusters, id)
		} else {
			parsed.Filters["clusters.cluster_id"] = append(parsed.Filters["clusters.cluster_id"], id)
		}
	}

	parsedClusterNames := parsed.Filters["clusters.name"]
	for _, clusterName := range parsedClusterNames {
		if !getAccessByClusterName(allRules, clusterName) {
			unAuthorizedClusters = append(unAuthorizedClusters, clusterName)
		}
	}

	// There is no cluster.cluster_id or cluster.name query argument.
	// In other words, the user requests all they have access to.
	if len(clusterIDs) == 0 && len(parsedClusterNames) == 0 {
		for mcName := range allRules {
			// Add the cluster to the filter if the user has get authentication. Note that if the user has get access
			// on all managed clusters, that gets handled at the beginning of the function.
			if getAccessByClusterName(allRules, mcName) {
				parsed.Filters["clusters.name"] = append(parsed.Filters["clusters.name"], mcName)
			}
		}
	}

	if len(unAuthorizedClusters) > 0 {
		return parsed, fmt.Errorf("%w: the following cluster filters are not authorized: %s",
			ErrForbidden, strings.Join(unAuthorizedClusters, ", "))
	}

	if len(parsed.Filters["clusters.name"]) == 0 && len(parsed.Filters["clusters.cluster_id"]) == 0 {
		return parsed, ErrNoAccess
	}

	return parsed, nil
}

// generateGetComplianceEventsQuery will return a SELECT query with results ready to be parsed by
// scanIntoComplianceEvent. The caller is responsible for adding filters to the query.
func generateGetComplianceEventsQuery(includeSpec bool) string {
	return fmt.Sprintf(`SELECT %s
FROM
  compliance_events
  LEFT JOIN clusters ON compliance_events.cluster_id = clusters.id
  LEFT JOIN parent_policies ON compliance_events.parent_policy_id = parent_policies.id
  LEFT JOIN policies ON compliance_events.policy_id = policies.id`,
		strings.Join(generateSelectedArgs(includeSpec), ", "),
	)
}

func generateSelectedArgs(includeSpec bool) []string {
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

	return selectArgs
}

// generate Headers for CSV. "." replace by "_"
// Example: parent_policies.namespace -> parent_policies_namespace
func getCsvHeader(includeSpec bool) []string {
	localSelectArgs := generateSelectedArgs(includeSpec)

	for i, arg := range localSelectArgs {
		localSelectArgs[i] = strings.ReplaceAll(arg, ".", "_")
	}

	return localSelectArgs
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
func getSingleComplianceEvent(db *sql.DB, w http.ResponseWriter,
	r *http.Request, config *rest.Config,
) {
	eventIDStr := strings.TrimPrefix(r.URL.Path, "/api/v1/compliance-events/")

	eventID, err := strconv.ParseUint(eventIDStr, 10, 64)
	if err != nil {
		writeErrMsgJSON(w, "The provided compliance event ID is invalid", http.StatusBadRequest)

		return
	}

	query := fmt.Sprintf("%s\nWHERE compliance_events.id = $1;", generateGetComplianceEventsQuery(true))

	row := db.QueryRowContext(r.Context(), query, eventID)
	if row.Err() != nil {
		log.Error(row.Err(), "Failed to query for the compliance event", "eventID", eventID)
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	complianceEvent, err := scanIntoComplianceEvent(row, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErrMsgJSON(w, "The requested compliance event was not found", http.StatusNotFound)

			return
		}

		log.Error(err, "Failed to unmarshal the database results", getPqErrKeyVals(err)...)
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	// Check auth for managedCluster GET verb
	isAllowed, err := canGetManagedCluster(config, complianceEvent.Cluster.Name)
	if err != nil {
		log.Error(err, `Failed to get the "get" authorization for the cluster`,
			"cluster", complianceEvent.Cluster.Name)
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	if !isAllowed {
		writeErrMsgJSON(w, "Forbidden", http.StatusForbidden)

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

// getPqErrKeyVals is a helper to add additional database error details to a log message. additionalKeyVals is provided
// as a convenience so that the keys don't need to be explicitly set to interface{} types when using the
// `getPqErrKeyVals(err, "key1", "val1")...“ syntax.
func getPqErrKeyVals(err error, additionalKeyVals ...interface{}) []interface{} {
	var pqErr *pq.Error

	if errors.As(err, &pqErr) {
		return append(
			[]interface{}{"dbMessage", pqErr.Message, "dbDetail", pqErr.Detail, "dbCode", pqErr.Code},
			additionalKeyVals...,
		)
	}

	return additionalKeyVals
}

func getClusterNameFromID(ctx context.Context, db *sql.DB, clusterID string) (name string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT name FROM clusters WHERE cluster_id = $1`, clusterID,
	).Scan(&name)
	if err != nil {
		return "", err
	}

	return name, nil
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
func getComplianceEvents(db *sql.DB, w http.ResponseWriter,
	r *http.Request, userConfig *rest.Config,
) {
	queryArgs, err := parseQueryArgs(r.Context(), r.URL.Query(), db, userConfig, false)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeErrMsgJSON(w, err.Error(), http.StatusForbidden)

			return
		}

		if errors.Is(err, ErrNoAccess) {
			response := ListResponse{
				Data: []ComplianceEvent{},
				Metadata: metadata{
					Page:    queryArgs.Page,
					Pages:   0,
					PerPage: queryArgs.PerPage,
					Total:   0,
				},
			}

			jsonResp, err := json.Marshal(response)
			if err != nil {
				log.Error(err, "Failed to marshal an empty response")
				writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

				return
			}

			if _, err = w.Write(jsonResp); err != nil {
				log.Error(err, "Error writing empty response")
				writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)
			}

			return
		}

		if errors.Is(err, ErrInvalidQueryArg) || errors.Is(err, ErrInvalidQueryArgValue) ||
			errors.Is(err, ErrInvalidSortOption) {
			writeErrMsgJSON(w, err.Error(), http.StatusBadRequest)

			return
		}

		writeErrMsgJSON(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// Note that the where clause could be an empty string if not filters were passed in the query arguments.
	whereClause, filterValues := getWhereClause(queryArgs)

	query := getComplianceEventsQuery(whereClause, queryArgs)

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
		log.Error(err, "Failed to get the count of compliance events", getPqErrKeyVals(err)...)
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
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}
}

// postComplianceEvent assumes you have a read lock already attained.
func postComplianceEvent(serverContext *ComplianceServerCtx, cfg *rest.Config, w http.ResponseWriter, r *http.Request) {
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

	if err := reqEvent.Validate(r.Context(), serverContext); err != nil {
		writeErrMsgJSON(w, err.Error(), http.StatusBadRequest)

		return
	}

	allowed, err := canRecordComplianceEvent(cfg, reqEvent.Cluster.Name, r)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
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

	clusterFK, err := GetClusterForeignKey(r.Context(), serverContext.DB, reqEvent.Cluster)
	if err != nil {
		log.Error(err, "error getting cluster foreign key", getPqErrKeyVals(err)...)
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	reqEvent.Event.ClusterID = clusterFK

	if reqEvent.ParentPolicy != nil {
		pfk, err := getParentPolicyForeignKey(r.Context(), serverContext, *reqEvent.ParentPolicy)
		if err != nil {
			log.Error(err, "error getting parent policy foreign key", getPqErrKeyVals(err)...)
			writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

			return
		}

		reqEvent.Event.ParentPolicyID = &pfk
	}

	policyFK, err := getPolicyForeignKey(r.Context(), serverContext, reqEvent.Policy)
	if err != nil {
		log.Error(err, "error getting policy foreign key", getPqErrKeyVals(err)...)
		writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

		return
	}

	reqEvent.Event.PolicyID = policyFK

	err = reqEvent.Create(r.Context(), serverContext.DB)
	if err != nil {
		if errors.Is(err, errDuplicateComplianceEvent) {
			writeErrMsgJSON(w, "The compliance event already exists", http.StatusConflict)

			return
		}

		var pqErr *pq.Error

		if errors.As(err, &pqErr) && pqErr.Code == postgresForeignKeyViolationCode {
			// This can only happen if the cache is out of date due to data loss in the database because if the
			// database ID is provided, it is validated against the database.
			log.Info(
				"Encountered a foreign key violation. Assuming the database lost data, so the cache is "+
					"being cleared",
				"message", pqErr.Message,
				"detail", pqErr.Detail,
			)

			// Temporarily upgrade the lock to a write lock
			serverContext.Lock.RUnlock()
			serverContext.Lock.Lock()
			serverContext.ParentPolicyToID = sync.Map{}
			serverContext.PolicyToID = sync.Map{}
			clusterKeyCache = sync.Map{}
			serverContext.Lock.Unlock()
			serverContext.Lock.RLock()
		} else {
			log.Error(err, "error inserting compliance event", getPqErrKeyVals(err)...)
		}

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

func getComplianceEventsQuery(whereClause string, queryArgs *queryOptions) string {
	// Getting CSV without the page argument
	// Query should fetch all rows (unlimited)
	if queryArgs.PerPage == 0 {
		return fmt.Sprintf(`%s%s
		ORDER BY %s %s;`,
			generateGetComplianceEventsQuery(queryArgs.IncludeSpec),
			whereClause,
			strings.Join(queryArgs.Sort, ", "),
			queryArgs.Direction,
		)
	}
	// Example query
	//   SELECT compliance_events.id, compliance_events.compliance, ...
	//     FROM compliance_events
	//   LEFT JOIN clusters ON compliance_events.cluster_id = clusters.id
	//   LEFT JOIN parent_policies ON compliance_events.parent_policy_id = parent_policies.id
	//   LEFT JOIN policies ON compliance_events.policy_id = policies.id
	//   WHERE (policies.name=$1 OR policies.name=$2) AND (policies.kind=$3)
	//   ORDER BY compliance_events.timestamp desc
	//   LIMIT 20
	//   OFFSET 0 ROWS;
	return fmt.Sprintf(`%s%s
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
}

func setCSVResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Disposition", "attachment; filename=reports.csv")
	w.Header().Set("Content-Type", "text/csv")
	// It's going to be divided into chunks. if the user don't get it all at once,
	// the user can receive one by one in the meantime
	w.Header().Set("Transfer-Encoding", "chunked")
}

func getComplianceEventsCSV(db *sql.DB, w http.ResponseWriter, r *http.Request,
	userConfig *rest.Config,
) {
	var writer *csv.Writer

	queryArgs, queryArgsErr := parseQueryArgs(r.Context(), r.URL.Query(), db, userConfig, true)
	if queryArgs != nil {
		headers := getCsvHeader(queryArgs.IncludeSpec)

		writer = csv.NewWriter(w)

		err := writer.Write(headers)
		if err != nil {
			log.Error(err, "Failed to write csv header")
			writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

			return
		}
	}

	if queryArgsErr != nil {
		if errors.Is(queryArgsErr, ErrNoAccess) {
			setCSVResponseHeaders(w)

			writer.Flush()

			return
		}

		if errors.Is(queryArgsErr, ErrInvalidQueryArg) || errors.Is(queryArgsErr, ErrInvalidQueryArgValue) ||
			errors.Is(queryArgsErr, ErrInvalidSortOption) {
			writeErrMsgJSON(w, queryArgsErr.Error(), http.StatusBadRequest)

			return
		}

		writeErrMsgJSON(w, queryArgsErr.Error(), http.StatusInternalServerError)

		return
	}

	// Note that the where clause could be an empty string if no filters were passed in the query arguments.
	whereClause, filterValues := getWhereClause(queryArgs)

	query := getComplianceEventsQuery(whereClause, queryArgs)

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

	for rows.Next() {
		ce, err := scanIntoComplianceEvent(rows, queryArgs.IncludeSpec)
		if err != nil {
			log.Error(err, "Failed to unmarshal the database results")
			writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

			return
		}

		stringValues := convertToCsvLine(ce, queryArgs.IncludeSpec)

		err = writer.Write(stringValues)
		if err != nil {
			log.Error(err, "Failed to write csv list")
			writeErrMsgJSON(w, "Internal Error", http.StatusInternalServerError)

			return
		}
	}

	setCSVResponseHeaders(w)

	writer.Flush()
}

func convertToCsvLine(ce *ComplianceEvent, includeSpec bool) []string {
	nilString := ""

	if ce.ParentPolicy == nil {
		ce.ParentPolicy = &ParentPolicy{
			KeyID:      0,
			Name:       "",
			Namespace:  "",
			Categories: nil,
			Controls:   nil,
			Standards:  nil,
		}
	}

	if ce.Event.ReportedBy == nil {
		ce.Event.ReportedBy = &nilString
	}

	if ce.Policy.Severity == nil {
		ce.Policy.Severity = &nilString
	}

	if ce.Policy.Namespace == nil {
		ce.Policy.Namespace = &nilString
	}

	values := []string{
		convertToString(ce.EventID),
		convertToString(ce.Event.Compliance),
		convertToString(ce.Event.Message),
		convertToString(ce.Event.Metadata),
		convertToString(*ce.Event.ReportedBy),
		convertToString(ce.Event.Timestamp),
		convertToString(ce.Cluster.ClusterID),
		convertToString(ce.Cluster.Name),
		convertToString(ce.ParentPolicy.KeyID),
		convertToString(ce.ParentPolicy.Name),
		convertToString(ce.ParentPolicy.Namespace),
		convertToString(ce.ParentPolicy.Categories),
		convertToString(ce.ParentPolicy.Controls),
		convertToString(ce.ParentPolicy.Standards),
		convertToString(ce.Policy.KeyID),
		convertToString(ce.Policy.APIGroup),
		convertToString(ce.Policy.Kind),
		convertToString(ce.Policy.Name),
		convertToString(*ce.Policy.Namespace),
		convertToString(*ce.Policy.Severity),
	}

	if includeSpec {
		values = append(values, convertToString(ce.Policy.Spec))
	}

	return values
}

func convertToString(v interface{}) string {
	switch vv := v.(type) {
	case *string:
		if vv == nil {
			return ""
		}

		return *vv
	case string:
		return vv
	case int32:
		// All int32 related id
		if int(vv) == 0 {
			return ""
		}

		return strconv.Itoa(int(vv))
	case time.Time:
		return vv.String()
	case pq.StringArray:
		// nil will be []
		return strings.Join(vv, ", ")
	case bool:
		return strconv.FormatBool(vv)
	case JSONMap:
		if vv == nil {
			return ""
		}

		jsonByte, err := json.MarshalIndent(vv, "", "  ")
		if err != nil {
			return ""
		}

		return string(jsonByte)
	default:
		// case nil:
		return fmt.Sprintf("%v", vv)
	}
}

// GetClusterForeignKey will return the database ID based on the cluster.ClusterID.
func GetClusterForeignKey(ctx context.Context, db *sql.DB, cluster Cluster) (int32, error) {
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

func getParentPolicyForeignKey(
	ctx context.Context, complianceServerCtx *ComplianceServerCtx, parent ParentPolicy,
) (int32, error) {
	if parent.KeyID != 0 {
		return parent.KeyID, nil
	}

	// Check cache
	parKey := parent.Key()

	key, ok := complianceServerCtx.ParentPolicyToID.Load(parKey)
	if ok {
		return key.(int32), nil
	}

	err := parent.GetOrCreate(ctx, complianceServerCtx.DB)
	if err != nil {
		return 0, err
	}

	complianceServerCtx.ParentPolicyToID.Store(parKey, parent.KeyID)

	return parent.KeyID, nil
}

func getPolicyForeignKey(ctx context.Context, complianceServerCtx *ComplianceServerCtx, pol Policy) (int32, error) {
	if pol.KeyID != 0 {
		return pol.KeyID, nil
	}

	// Check cache
	polKey := pol.Key()

	key, ok := complianceServerCtx.PolicyToID.Load(polKey)
	if ok {
		return key.(int32), nil
	}

	err := pol.GetOrCreate(ctx, complianceServerCtx.DB)
	if err != nil {
		return 0, err
	}

	complianceServerCtx.PolicyToID.Store(polKey, pol.KeyID)

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
