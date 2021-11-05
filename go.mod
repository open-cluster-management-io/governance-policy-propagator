module github.com/open-cluster-management/governance-policy-propagator

go 1.16

require (
	github.com/avast/retry-go/v3 v3.1.1
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/go-logr/logr v0.4.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/open-cluster-management/api v0.0.0-20210527013639-a6845f2ebcb1
	github.com/open-cluster-management/go-template-utils v1.3.0
	github.com/open-cluster-management/multicloud-operators-placementrule v1.2.4-0-20210816-699e5
	github.com/prometheus/client_golang v1.11.0
	k8s.io/api v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.9.2
)

replace (
	github.com/go-logr/logr v0.1.0 => github.com/go-logr/logr v0.2.1
	github.com/go-logr/logr v0.2.0 => github.com/go-logr/logr v0.2.1
	github.com/go-logr/zapr v0.1.0 => github.com/go-logr/zapr v0.2.0
	k8s.io/client-go => k8s.io/client-go v0.21.3
)
