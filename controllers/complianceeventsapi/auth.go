// Copyright Contributors to the Open Cluster Management project
package complianceeventsapi

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	authzv1 "k8s.io/api/authorization/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserverx509 "k8s.io/apiserver/pkg/authentication/request/x509"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// parseToken will return the token string in the Authorization header.
func parseToken(req *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer"))
}

// getClientFromToken will generate a Kubernetes client using the input config and token. No authentication data from
// the input config is used in the generated client.
func getClientFromToken(cfg *rest.Config, token string) (*kubernetes.Clientset, error) {
	userConfig := &rest.Config{
		Host:    cfg.Host,
		APIPath: cfg.APIPath,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile:     cfg.TLSClientConfig.CAFile,
			CAData:     cfg.TLSClientConfig.CAData,
			ServerName: cfg.TLSClientConfig.ServerName,
			Insecure:   cfg.TLSClientConfig.Insecure,
		},
		BearerToken: token,
	}

	return kubernetes.NewForConfig(userConfig)
}

// canRecordComplianceEvent will perform certificate or token authentication and perform a subject access review to
// ensure the input user has patch access to patch the policy status in the managed cluster namespace. An error is
// returned if the authorization could not be determined. Note that authenticatedClient and authenticator can be nil
// if certificate authentication isn't used. If both certificate and token authentication is present, certificate takes
// precedence.
func canRecordComplianceEvent(
	cfg *rest.Config,
	authenticatedClient *kubernetes.Clientset,
	authenticator *apiserverx509.Authenticator,
	clusterName string,
	req *http.Request,
) (bool, error) {
	postRules := authzv1.ResourceAttributes{
		Group:       "policy.open-cluster-management.io",
		Version:     "v1",
		Resource:    "policies",
		Verb:        "patch",
		Namespace:   clusterName,
		Subresource: "status",
	}

	// req.TLS.PeerCertificates will be empty if certificate authentication is not enabled (e.g. endpoint is not HTTPS)
	if req.TLS != nil && len(req.TLS.PeerCertificates) > 0 {
		resp, ok, err := authenticator.AuthenticateRequest(req)
		if err != nil {
			if errors.As(err, &x509.UnknownAuthorityError{}) || errors.As(err, &x509.CertificateInvalidError{}) {
				return false, ErrNotAuthorized
			}

			return false, err
		}

		if !ok {
			return false, ErrNotAuthorized
		}

		review, err := authenticatedClient.AuthorizationV1().SubjectAccessReviews().Create(
			req.Context(),
			&authzv1.SubjectAccessReview{
				Spec: authzv1.SubjectAccessReviewSpec{
					ResourceAttributes: &postRules,
					User:               resp.User.GetName(),
					Groups:             resp.User.GetGroups(),
					UID:                resp.User.GetUID(),
				},
			},
			metav1.CreateOptions{},
		)
		if err != nil {
			return false, err
		}

		if !review.Status.Allowed {
			log.V(0).Info(
				"The user is not authorized to record a compliance event",
				"cluster", clusterName,
				"user", resp.User.GetName(),
			)
		}

		return review.Status.Allowed, nil
	}

	token := parseToken(req)
	if token == "" {
		return false, ErrNotAuthorized
	}

	userClient, err := getClientFromToken(cfg, token)
	if err != nil {
		return false, err
	}

	result, err := userClient.AuthorizationV1().SelfSubjectAccessReviews().Create(
		req.Context(),
		&authzv1.SelfSubjectAccessReview{
			Spec: authzv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &postRules,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		if k8serrors.IsUnauthorized(err) {
			return false, ErrNotAuthorized
		}

		return false, err
	}

	if !result.Status.Allowed {
		log.V(0).Info(
			"The user is not authorized to record a compliance event",
			"cluster", clusterName,
			"user", getTokenUsername(token),
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

// getCertAuthenticator returns an Authenticator that can validate that an input certificate is signed by the API
// server represented in the input cfg and that the certificate can be used for client authentication (e.g. key usage).
func getCertAuthenticator(cfg *rest.Config) (*apiserverx509.Authenticator, error) {
	p, err := dynamiccertificates.NewStaticCAContent("client-ca", cfg.CAData)
	if err != nil {
		return nil, err
	}

	// This is the same approach taken by kube-rbac-proxy.
	authenticator := apiserverx509.NewDynamic(p.VerifyOptions, apiserverx509.CommonNameUserConversion)

	return authenticator, nil
}
