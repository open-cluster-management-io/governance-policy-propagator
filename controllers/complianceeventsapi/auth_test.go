package complianceeventsapi

import (
	"testing"
)

func TestGetTokenUsername(t *testing.T) {
	t.Parallel()

	token := "part1.eyJpc3MiOiJrdWJlcm5ldGVzL3NlcnZpY2VhY2NvdW50Iiwia3ViZXJuZXRlcy5pby9zZXJ2aWNlYWNjb3VudC9uYW1lc" +
		"3BhY2UiOiJvcGVuLWNsdXN0ZXItbWFuYWdlbWVudCIsImt1YmVybmV0ZXMuaW8vc2VydmljZWFjY291bnQvc2VjcmV0Lm5hbWUiOiJnb3Zl" +
		"cm5hbmNlLXBvbGljeS1wcm9wYWdhdG9yIiwia3ViZXJuZXRlcy5pby9zZXJ2aWNlYWNjb3VudC9zZXJ2aWNlLWFjY291bnQubmFtZSI6Imdv" +
		"dmVybmFuY2UtcG9saWN5LXByb3BhZ2F0b3IiLCJrdWJlcm5ldGVzLmlvL3NlcnZpY2VhY2NvdW50L3NlcnZpY2UtYWNjb3VudC51aWQiOiJ" +
		"lMjQzZDBlNi03YjJkLTRjZjQtYmExMC1mMTE5NWQwMGUxZTYiLCJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6b3Blbi1jbHVzdGVyLW" +
		"1hbmFnZW1lbnQ6Z292ZXJuYW5jZS1wb2xpY3ktcHJvcGFnYXRvciJ9.part3"

	username := getTokenUsername(token)
	expected := "system:serviceaccount:open-cluster-management:governance-policy-propagator"

	if username != expected {
		t.Fatalf("Expected %s but got %s", expected, username)
	}
}
