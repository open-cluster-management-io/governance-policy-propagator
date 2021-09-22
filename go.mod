module github.com/open-cluster-management/governance-policy-propagator

go 1.16

require (
	github.com/avast/retry-go/v3 v3.1.1
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/go-logr/logr v0.4.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/open-cluster-management/api v0.0.0-20200610161514-939cead3902c
	github.com/open-cluster-management/go-template-utils v1.2.2
	github.com/operator-framework/operator-sdk v0.19.4
	github.com/prometheus/client_golang v1.5.1
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.20.5
	k8s.io/apimachinery v0.20.5
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.6.2
)

replace (
	github.com/go-logr/logr => github.com/go-logr/logr v0.2.1
	github.com/go-logr/zapr => github.com/go-logr/zapr v0.2.0
	k8s.io/client-go => k8s.io/client-go v0.20.5
)
