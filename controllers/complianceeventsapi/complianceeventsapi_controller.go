// Copyright Contributors to the Open Cluster Management project

package complianceeventsapi

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	// Required to activate the Postgres driver
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

//go:embed migrations
var migrationsFS embed.FS

const (
	ControllerName       = "compliance-events-api"
	DBSecretName         = "governance-policy-database"
	WatchNamespaceEnvVar = "WATCH_NAMESPACE_COMPLIANCE_EVENTS_STORE"
)

var (
	log                = ctrl.Log.WithName(ControllerName)
	scheme             = k8sruntime.NewScheme()
	ErrCouldNotStart   = errors.New("the compliance events API manager could not be started")
	ErrInvalidDBSecret = errors.New("the governance-policy-database secret is invalid")
	migrationsSource   source.Driver
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	var err error
	migrationsSource, err = iofs.New(migrationsFS, "migrations")

	utilruntime.Must(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComplianceEventsAPIReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&corev1.Secret{}).
		Complete(r)
}

// blank assignment to verify that ComplianceEventsAPIReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &ComplianceEventsAPIReconciler{}

// ComplianceEventsAPIReconciler is responsible for managing the compliance events history database migrations and
// starting up the compliance events API.
type ComplianceEventsAPIReconciler struct {
	client.Client
	Scheme *k8sruntime.Scheme
	// TempDir is used for temporary files such as a custom CA to use to verify the Postgres TLS connection. The
	// caller is responsible for cleaning it up after the controller stops.
	TempDir       string
	connectionURL string
}

// WARNING: In production, this should be namespaced to the namespace the controller is running in.
//+kubebuilder:rbac:groups=core,resources=secrets,resourceNames=governance-policy-database,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=create

// Reconcile watches the governance-policy-database secret in the controller namespace. When present and valid, the
// compliance events store functionality will turn on.
func (r *ComplianceEventsAPIReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("secretNamespace", request.Namespace, "secret", request.Name)
	log.Info("Reconciling a Secret")

	// The cache configuration of cache.ByObject should prevent this from happening, but add this as a precaution.
	if request.Name != DBSecretName {
		log.Info("Got a reconciliation request for an unexpected Secret. This should have been filtered out.")

		return reconcile.Result{}, nil
	}

	dbSecret := corev1.Secret{}

	err := r.Get(ctx, request.NamespacedName, &dbSecret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}

		return reconcile.Result{}, err
	}

	parsedConnectionURL, err := parseDBSecret(&dbSecret, r.TempDir)
	if errors.Is(err, ErrInvalidDBSecret) {
		log.Error(err, "Will retry once the invalid secret is updated")

		return reconcile.Result{}, nil
	} else if err != nil {
		log.Error(err, "Will retry in 30 seconds due to the error")

		return reconcile.Result{RequeueAfter: time.Second * 30}, nil
	}

	// r.connectionURL is only ever set if the database migration succeeded previously so if the URL is the same then
	// skip any further work.
	if r.connectionURL == parsedConnectionURL {
		return reconcile.Result{}, nil
	}

	m, err := migrate.NewWithSourceInstance("iofs", migrationsSource, parsedConnectionURL)
	if err != nil {
		msg := "Failed to initialize the database migration client. Will retry in 30 seconds."
		log.Error(err, msg)

		_ = r.sendDBErrorEvent(ctx, &dbSecret, msg)

		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	defer m.Close()

	err = m.Up()
	if err != nil && err.Error() == "no change" {
		log.Info("The database schema is up to date")
	} else if err != nil {
		msg := "Failed to perform the database migration. The compliance events endpoint will not start until this " +
			"is resolved."

		log.Error(err, msg)

		_ = r.sendDBErrorEvent(ctx, &dbSecret, msg)

		return reconcile.Result{}, nil
	} else {
		// The errors don't need to be checked because we know the database migration was successful so there is a
		// valid version assigned.
		version, _, _ := m.Version()
		msg := fmt.Sprintf("The compliance events database schema was successfully updated to version %d", version)
		log.Info(msg)

		_ = r.sendDBEvent(ctx, &dbSecret, "Normal", "OCMComplianceEventsDB", msg)
	}

	r.connectionURL = parsedConnectionURL

	return reconcile.Result{}, nil
}

func parseDBSecret(dbSecret *corev1.Secret, tempDirPath string) (string, error) {
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

func (r *ComplianceEventsAPIReconciler) sendDBEvent(
	ctx context.Context, dbSecret *corev1.Secret, eventType, reason, msg string,
) error {
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("compliance-events-api.%x", time.Now().UnixNano()),
			Namespace: dbSecret.Namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       dbSecret.Kind,
			Namespace:  dbSecret.Namespace,
			Name:       dbSecret.Name,
			UID:        dbSecret.UID,
			APIVersion: dbSecret.APIVersion,
		},
		Type:    eventType,
		Reason:  reason,
		Message: msg,
		Source: corev1.EventSource{
			Component: ControllerName,
		},
		ReportingController: ControllerName,
	}

	err := r.Create(ctx, event)
	if err != nil {
		log.Error(err, "Failed to send a Kubernetes warning event")
	}

	return err
}

func (r *ComplianceEventsAPIReconciler) sendDBErrorEvent(
	ctx context.Context, dbSecret *corev1.Secret, msg string,
) error {
	fullMsg := msg + " See the governance-policy-propagator logs for more details."

	return r.sendDBEvent(ctx, dbSecret, "Warning", "OCMComplianceEventsDBError", fullMsg)
}

func StartManager(ctx context.Context, k8sConfig *rest.Config, enableLeaderElection bool) error {
	complianceEventsNamespace, _ := os.LookupEnv(WatchNamespaceEnvVar)
	if complianceEventsNamespace == "" {
		complianceEventsNamespace = "open-cluster-management"
	}

	complianceEventsMgr, err := ctrl.NewManager(
		k8sConfig,
		manager.Options{
			Namespace: complianceEventsNamespace,
			Scheme:    scheme,
			// Disable the optional endpoints
			MetricsBindAddress:         "0",
			HealthProbeBindAddress:     "0",
			LeaderElection:             enableLeaderElection,
			LeaderElectionID:           "governance-compliance-events-api.open-cluster-management.io",
			LeaderElectionResourceLock: "leases",
			Cache: cache.Options{
				ByObject: map[client.Object]cache.ByObject{
					// Set a field selector so that a watch on secrets will be limited to just the secret with
					// the database connection information.
					&corev1.Secret{}: {
						Field: fields.SelectorFromSet(fields.Set{"metadata.name": DBSecretName}),
					},
				},
			},
		},
	)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCouldNotStart, err)
	}

	tempDir, err := os.MkdirTemp("", "compliance-events-store")
	if err != nil {
		return err
	}

	if err = (&ComplianceEventsAPIReconciler{
		Client:  complianceEventsMgr.GetClient(),
		Scheme:  complianceEventsMgr.GetScheme(),
		TempDir: tempDir,
	}).SetupWithManager(complianceEventsMgr); err != nil {
		return fmt.Errorf("%w: %w", ErrCouldNotStart, err)
	}

	log.V(1).Info(
		"Starting the compliance events store feature. To enable this functionality, ensure the Postgres "+
			"connection secret is in the controller namespace.",
		"secretName", DBSecretName,
		"namespace", complianceEventsNamespace,
	)

	// complianceEventsMgr.Start is blocking so it must be run in a goroutine.
	go func() {
		defer func() {
			err := os.RemoveAll(tempDir)
			if err != nil {
				log.Error(err, "Failed to clean up the temporary directory", "path", tempDir)
			}
		}()

		err := complianceEventsMgr.Start(ctx)
		if err != nil {
			log.Error(err, "Unable to start the compliance events store")
			os.Exit(1)
		}
	}()

	return nil
}
