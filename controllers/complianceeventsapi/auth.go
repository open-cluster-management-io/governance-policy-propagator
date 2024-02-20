// Copyright Contributors to the Open Cluster Management project
package complianceeventsapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/stolostron/rbac-api-utils/pkg/rbac"
	authzv1 "k8s.io/api/authorization/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func getManagedClusterRules(userChangedConfig *rest.Config, managedClusterNames []string,
) (map[string][]string, error) {
	kclient, err := kubernetes.NewForConfig(userChangedConfig)
	if err != nil {
		log.Error(err, "Failed to create a Kubernetes client with the user token")

		return nil, err
	}

	managedClusterGR := schema.GroupResource{
		Group:    "cluster.open-cluster-management.io",
		Resource: "managedclusters",
	}

	// key is managedClusterName and value is verbs.
	// ex: {"managed1": ["get"], "managed2": ["list","create"], "managed3": []}
	return rbac.GetResourceAccess(kclient, managedClusterGR, managedClusterNames, "")
}

func canGetManagedCluster(userChangedConfig *rest.Config, managedClusterName string,
) (bool, error) {
	allRules, err := getManagedClusterRules(userChangedConfig, []string{managedClusterName})
	if err != nil {
		return false, err
	}

	return getAccessByClusterName(allRules, managedClusterName), nil
}

func getAccessByClusterName(allManagedClusterRules map[string][]string, clusterName string) bool {
	starRules, ok := allManagedClusterRules["*"]
	if ok && slices.Contains(starRules, "get") || slices.Contains(starRules, "*") {
		return true
	}

	rules, ok := allManagedClusterRules[clusterName]
	if ok && slices.Contains(rules, "get") || slices.Contains(rules, "*") {
		return true
	}

	return false
}

// parseToken will return the token string in the Authorization header.
func parseToken(req *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer"))
}

// canRecordComplianceEvent will perform token authentication and perform a self subject access review to
// ensure the input user has patch access to patch the policy status in the managed cluster namespace. An error is
// returned if the authorization could not be determined.
func canRecordComplianceEvent(cfg *rest.Config, clusterName string, req *http.Request) (bool, error) {
	userConfig, err := getUserKubeConfig(cfg, req)
	if err != nil {
		return false, err
	}

	userClient, err := kubernetes.NewForConfig(userConfig)
	if err != nil {
		return false, err
	}

	result, err := userClient.AuthorizationV1().SelfSubjectAccessReviews().Create(
		req.Context(),
		&authzv1.SelfSubjectAccessReview{
			Spec: authzv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authzv1.ResourceAttributes{
					Group:       "policy.open-cluster-management.io",
					Version:     "v1",
					Resource:    "policies",
					Verb:        "patch",
					Namespace:   clusterName,
					Subresource: "status",
				},
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		if k8serrors.IsUnauthorized(err) {
			return false, ErrUnauthorized
		}

		return false, err
	}

	if !result.Status.Allowed {
		log.V(0).Info(
			"The user is not authorized to record a compliance event",
			"cluster", clusterName,
			"user", getTokenUsername(userConfig.BearerToken),
		)
	}

	return result.Status.Allowed, nil
}

// getTokenUsername will parse the token and return the username. If the token is invalid, an empty string is returned.
func getTokenUsername(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		log.V(2).Info("The token does not have the expected three parts")

		return ""
	}

	userInfoBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		log.V(2).Info("The token does not have valid base64")

		return ""
	}

	userInfo := map[string]interface{}{}

	err = json.Unmarshal(userInfoBytes, &userInfo)
	if err != nil {
		log.V(2).Info("The token does not have valid JSON")

		return ""
	}

	username, ok := userInfo["sub"].(string)
	if !ok {
		return ""
	}

	return username
}

func getUserKubeConfig(config *rest.Config, r *http.Request) (*rest.Config, error) {
	userConfig := &rest.Config{
		Host:    config.Host,
		APIPath: config.APIPath,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile:     config.TLSClientConfig.CAFile,
			CAData:     config.TLSClientConfig.CAData,
			ServerName: config.TLSClientConfig.ServerName,
			// For testing
			Insecure: config.TLSClientConfig.Insecure,
		},
	}

	userConfig.BearerToken = parseToken(r)

	if userConfig.BearerToken == "" {
		return nil, ErrUnauthorized
	}

	return userConfig, nil
}
