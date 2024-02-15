// Copyright Contributors to the Open Cluster Management project

package complianceeventsapi

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	// Required to activate the Postgres driver
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

//go:embed migrations
var migrationsFS embed.FS

const (
	ControllerName       = "compliance-events-api"
	DBSecretName         = "governance-policy-database"
	WatchNamespaceEnvVar = "WATCH_NAMESPACE_COMPLIANCE_EVENTS_STORE"
)

var (
	log                     = ctrl.Log.WithName(ControllerName)
	ErrInvalidDBSecret      = errors.New("the governance-policy-database secret is invalid")
	ErrInvalidConnectionURL = errors.New("the database connection URL is invalid")
	ErrDBConnectionFailed   = errors.New("the compliance events database could not be connected to")
	migrationsSource        source.Driver
	gvkSecret               = schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
)

func init() {
	var err error
	migrationsSource, err = iofs.New(migrationsFS, "migrations")

	utilruntime.Must(err)
}

// ComplianceServerCtx acts as a "global" database instance that all required controllers share. The
// ComplianceDBSecretReconciler reconciler is responsible for updating the DB field if the connection info gets added
// or changes. MonitorDatabaseConnection will periodically check the health of the database connection and monitor
// the Queue. See MonitorDatabaseConnection for more information.
type ComplianceServerCtx struct {
	// A write lock is used when the database connection changes and the DB object needs to be replaced.
	// A read lock should be used when the DB is accessed.
	Lock           sync.RWMutex
	DB             *sql.DB
	Queue          workqueue.Interface
	needsMigration bool
	// Required to run a migration after the database connection changed or the feature was enabled.
	connectionURL string
	// These caches get reset after a database migration due to a connection drop and reconnect.
	ParentPolicyToID sync.Map
	PolicyToID       sync.Map
}

// NewComplianceServerCtx returns a ComplianceServerCtx with initialized values. It does not start a connection
// but does validate the connection URL for syntax. If the connection URL is not provided or is invalid,
// ErrInvalidConnectionURL is returned.
func NewComplianceServerCtx(dbConnectionURL string) (*ComplianceServerCtx, error) {
	var db *sql.DB
	var err error

	if dbConnectionURL == "" {
		err = ErrInvalidConnectionURL
	} else {
		var openErr error
		// As of the writing of this code, sql.Open doesn't create a connection. db.Ping will though, so this
		// should never fail unless the connection URL is invalid to the Postgres driver.
		db, openErr = sql.Open("postgres", dbConnectionURL)
		if openErr != nil {
			err = fmt.Errorf("%w: %w", ErrInvalidConnectionURL, err)
		}
	}

	return &ComplianceServerCtx{
		Lock:          sync.RWMutex{},
		Queue:         workqueue.New(),
		connectionURL: dbConnectionURL,
		DB:            db,
	}, err
}

// ComplianceDBSecretReconciler is responsible for managing the compliance events history database migrations and
// keeping the shared database connection up to date.
type ComplianceDBSecretReconciler struct {
	DynamicWatcher k8sdepwatches.DynamicWatcher
	Client         *kubernetes.Clientset
	// TempDir is used for temporary files such as a custom CA to use to verify the Postgres TLS connection. The
	// caller is responsible for cleaning it up after the controller stops.
	TempDir             string
	ConnectionURL       string
	ComplianceServerCtx *ComplianceServerCtx
}

// WARNING: In production, this should be namespaced to the namespace the controller is running in.
//+kubebuilder:rbac:groups=core,resources=secrets,resourceNames=governance-policy-database,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=create
//+kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create

// Reconcile watches the governance-policy-database secret in the controller namespace. On updates it'll trigger
// a database migration and update the shared database connection.
func (r *ComplianceDBSecretReconciler) Reconcile(
	ctx context.Context, watcher k8sdepwatches.ObjectIdentifier,
) (ctrl.Result, error) {
	log := log.WithValues("secretNamespace", watcher.Namespace, "secret", watcher.Name)
	log.Info("Reconciling a Secret")

	// The watch configuration should prevent this from happening, but add this as a precaution.
	if watcher.Name != DBSecretName {
		log.Info("Got a reconciliation request for an unexpected Secret. This should have been filtered out.")

		return reconcile.Result{}, nil
	}

	var parsedConnectionURL string

	dbSecret, err := r.DynamicWatcher.GetFromCache(gvkSecret, watcher.Namespace, watcher.Name)
	if dbSecret == nil || errors.Is(err, k8sdepwatches.ErrNoCacheEntry) {
		parsedConnectionURL = ""
	} else if err != nil {
		return reconcile.Result{}, err
	} else {
		var typedDBSecret corev1.Secret

		err := k8sruntime.DefaultUnstructuredConverter.FromUnstructured(dbSecret.UnstructuredContent(), &typedDBSecret)
		if err != nil {
			log.Error(err, "The cached database secret could not be converted to a typed secret")

			return reconcile.Result{}, nil
		}

		parsedConnectionURL, err = ParseDBSecret(&typedDBSecret, r.TempDir)
		if errors.Is(err, ErrInvalidDBSecret) {
			log.Error(err, "Will retry once the invalid secret is updated")

			parsedConnectionURL = ""
		} else if err != nil {
			log.Error(err, "Will retry in 30 seconds due to the error")

			return reconcile.Result{RequeueAfter: time.Second * 30}, nil
		}
	}

	if r.ConnectionURL != parsedConnectionURL {
		log.Info(
			"The database connection URL has changed. Will handle missed database entries during downtime.",
		)

		r.ConnectionURL = parsedConnectionURL

		r.ComplianceServerCtx.Lock.Lock()
		defer r.ComplianceServerCtx.Lock.Unlock()

		// Need the connection URL for the migration.
		r.ComplianceServerCtx.connectionURL = r.ConnectionURL

		if parsedConnectionURL == "" {
			r.ComplianceServerCtx.DB = nil
		} else {
			// As of the writing of this code, sql.Open doesn't create a connection. db.Ping will though, so this
			// should never fail unless the connection URL is invalid to the Postgres driver.
			db, err := sql.Open("postgres", r.ConnectionURL)
			if err != nil {
				log.Error(
					err,
					"The Postgres connection URL could not be parsed by the driver. Try updating the secret.",
				)
			}

			// This may be nil and that is intentional.
			r.ComplianceServerCtx.DB = db
			// Once the connection URL changes, a migration is required in the event this is a new database or the
			// propagator was started when the database was offline and it was not up to date.
			// If the migration fails, let MonitorDatabaseConnection handle it.
			_ = r.ComplianceServerCtx.MigrateDB(ctx, r.Client, watcher.Namespace)
		}
	}

	return reconcile.Result{}, nil
}

// MonitorDatabaseConnection will check the database connection health every 20 seconds. If healthy, it will migrate
// the database if necessary, and send any reconcile requests to the replicated policy controller from
// complianceServerCtx.Queue. To stop MonitorDatabaseConnection, cancel the input context.
func MonitorDatabaseConnection(
	ctx context.Context,
	complianceServerCtx *ComplianceServerCtx,
	client *kubernetes.Clientset,
	controllerNamespace string,
	reconcileRequests chan<- event.GenericEvent,
) {
	for {
		sleep, cancelSleep := context.WithTimeout(context.Background(), time.Second*20)

		log.V(3).Info("Sleeping for 20 seconds until the next database check")

		select {
		case <-ctx.Done():
			complianceServerCtx.Queue.ShutDown()
			cancelSleep()

			return
		case <-sleep.Done():
			// Satisfy the linter, but in reality, this is a noop.
			cancelSleep()
		}

		complianceServerCtx.Lock.RLock()

		if complianceServerCtx.DB == nil {
			complianceServerCtx.Lock.RUnlock()

			continue
		}

		if !complianceServerCtx.needsMigration && complianceServerCtx.Queue.Len() == 0 {
			complianceServerCtx.Lock.RUnlock()

			continue
		}

		if err := complianceServerCtx.DB.PingContext(ctx); err != nil {
			complianceServerCtx.Lock.RUnlock()

			log.Info("The database connection failed: " + err.Error())

			continue
		}

		complianceServerCtx.Lock.RUnlock()

		if complianceServerCtx.needsMigration {
			complianceServerCtx.Lock.Lock()
			err := complianceServerCtx.MigrateDB(ctx, client, controllerNamespace)
			complianceServerCtx.Lock.Unlock()

			if err != nil {
				continue
			}
		}

		log.V(3).Info(
			"The compliance database is up. Checking for queued up reconcile requests.",
			"queueLength", complianceServerCtx.Queue.Len(),
		)

		sendLogMsg := complianceServerCtx.Queue.Len() > 0

		for complianceServerCtx.Queue.Len() > 0 {
			request, shutdown := complianceServerCtx.Queue.Get()

			if namespacedName, ok := request.(types.NamespacedName); ok {
				reconcileRequests <- event.GenericEvent{
					Object: &common.GuttedObject{
						TypeMeta: metav1.TypeMeta{
							APIVersion: policyv1.GroupVersion.String(),
							Kind:       "Policy",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      namespacedName.Name,
							Namespace: namespacedName.Namespace,
						},
					},
				}
			}

			complianceServerCtx.Queue.Done(request)

			// The queue should never get shutdown and still reach here which is why it's an info log. We need to
			// know about it if it happens unexpectedly.
			if shutdown {
				log.Info("The queue was shutdown. Exiting MonitorDatabaseConnection.")

				return
			}
		}

		if sendLogMsg {
			log.V(1).Info(
				"Done sending queued reconcile requests. Sleeping for 20 seconds until the next database check.",
				"queueLength", complianceServerCtx.Queue.Len(),
			)
		}
	}
}

// MigrateDB will perform a database migration if required and send Kubernetes events if the migration fails.
// ErrDBConnectionFailed will be returned if the database connection failed. Obtain a write lock before calling
// this method if multiple goroutines use this ComplianceServerCtx instance.
func (c *ComplianceServerCtx) MigrateDB(
	ctx context.Context, client *kubernetes.Clientset, controllerNamespace string,
) error {
	c.needsMigration = true

	if c.connectionURL == "" {
		return fmt.Errorf("%w: the connection URL is not set", ErrDBConnectionFailed)
	}

	m, err := migrate.NewWithSourceInstance("iofs", migrationsSource, c.connectionURL)
	if err != nil {
		msg := "Failed to initialize the database migration client"
		log.Error(err, msg)

		_ = sendDBErrorEvent(ctx, client, controllerNamespace, msg)

		return fmt.Errorf("%w: %w", ErrDBConnectionFailed, err)
	}

	defer m.Close()

	err = m.Up()
	if err != nil && err.Error() == "no change" {
		log.Info("The database schema is up to date")
	} else if err != nil {
		msg := "Failed to perform the database migration. The compliance events endpoint will not start until this " +
			"is resolved."

		log.Error(err, msg)

		_ = sendDBErrorEvent(ctx, client, controllerNamespace, msg)

		return fmt.Errorf("%w: %w", ErrDBConnectionFailed, err)
	} else {
		// The errors don't need to be checked because we know the database migration was successful so there is a
		// valid version assigned.
		version, _, _ := m.Version()
		// The cache gets reset after a migration in case the database changed. If the database
		// was restored to an older backup, then the propagator needs to restart to clear the cache.
		c.ParentPolicyToID = sync.Map{}
		c.PolicyToID = sync.Map{}

		msg := fmt.Sprintf("The compliance events database schema was successfully updated to version %d", version)
		log.Info(msg)

		_ = sendDBEvent(ctx, client, controllerNamespace, "Normal", "OCMComplianceEventsDB", msg)
	}

	c.needsMigration = false

	return nil
}

// ParseDBSecret will parse the input database secret and return a connection URL. If the secret contains invalid
// connection information, then ErrInvalidDBSecret is returned.
func ParseDBSecret(dbSecret *corev1.Secret, tempDirPath string) (string, error) {
	var connectionURL *url.URL
	var err error

	if dbSecret.Data["connectionURL"] != nil {
		connectionURL, err = url.Parse(string(dbSecret.Data["connectionURL"]))
		if err != nil {
			err := fmt.Errorf(
				"%w: failed to parse the connectionURL value, but not logging the error to protect sensitive data",
				ErrInvalidDBSecret,
			)

			return "", err
		}
	} else {
		if dbSecret.Data["user"] == nil {
			return "", fmt.Errorf("%w: no user value was provided", ErrInvalidDBSecret)
		}

		user := string(dbSecret.Data["user"])

		if dbSecret.Data["password"] == nil {
			return "", fmt.Errorf("%w: no password value was provided", ErrInvalidDBSecret)
		}

		password := string(dbSecret.Data["password"])

		if dbSecret.Data["host"] == nil {
			return "", fmt.Errorf("%w: no host value was provided", ErrInvalidDBSecret)
		}

		host := string(dbSecret.Data["host"])

		var port string

		if dbSecret.Data["port"] == nil {
			log.Info("No port value was provided. Using the default 5432.")
			port = "5432"
		} else {
			port = string(dbSecret.Data["port"])
		}

		if dbSecret.Data["dbname"] == nil {
			return "", fmt.Errorf("%w: no dbname value was provided", ErrInvalidDBSecret)
		}

		dbName := string(dbSecret.Data["dbname"])

		var sslMode string

		if dbSecret.Data["sslmode"] == nil {
			log.Info("No sslmode value was provided. Using the default sslmode=verify-full.")
			sslMode = "verify-full"
		} else {
			sslMode = string(dbSecret.Data["sslmode"])
		}

		connectionURL = &url.URL{
			Scheme:   "postgresql",
			User:     url.UserPassword(user, password),
			Host:     fmt.Sprintf("%s:%s", host, port),
			Path:     dbName,
			RawQuery: "sslmode=" + url.QueryEscape(sslMode),
		}
	}

	if !strings.Contains(connectionURL.RawQuery, "connect_timeout=") {
		if connectionURL.RawQuery != "" {
			connectionURL.RawQuery += "&"
		}

		// This is important or else db.Ping() takes too long if the connection is down.
		connectionURL.RawQuery += "connect_timeout=5"
	}

	if dbSecret.Data["ca"] != nil {
		caPath := path.Join(tempDirPath, "db-ca.crt")

		err := os.WriteFile(caPath, dbSecret.Data["ca"], 0o600)
		if err != nil {
			return "", fmt.Errorf("failed to write the custom root CA specified in the secret: %w", err)
		}

		if connectionURL.RawQuery != "" {
			connectionURL.RawQuery += "&"
		}

		connectionURL.RawQuery += "sslrootcert=" + url.QueryEscape(caPath)
	}

	if !strings.Contains(connectionURL.RawQuery, "sslmode=verify-full") {
		log.Info(
			"The configured Postgres connection URL does not specify sslmode=verify-full. Please consider using a " +
				"more secure connection.",
		)
	}

	return connectionURL.String(), nil
}

func sendDBEvent(
	ctx context.Context, client *kubernetes.Clientset, namespace, eventType, reason, msg string,
) error {
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("compliance-events-api.%x", time.Now().UnixNano()),
			Namespace: namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "Secret",
			Namespace:  namespace,
			Name:       DBSecretName,
			APIVersion: "v1",
		},
		Type:    eventType,
		Reason:  reason,
		Message: msg,
		Source: corev1.EventSource{
			Component: ControllerName,
		},
		ReportingController: ControllerName,
	}

	_, err := client.CoreV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{})
	if err != nil {
		log.Error(err, "Failed to send a Kubernetes warning event")
	}

	return err
}

func sendDBErrorEvent(ctx context.Context, client *kubernetes.Clientset, namespace, msg string) error {
	fullMsg := msg + " See the governance-policy-propagator logs for more details."

	return sendDBEvent(ctx, client, namespace, "Warning", "OCMComplianceEventsDBError", fullMsg)
}
