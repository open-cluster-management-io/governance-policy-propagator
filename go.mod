module github.com/open-cluster-management/governance-policy-propagator

go 1.14

require (
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/google/go-cmp v0.5.1 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.1
	github.com/open-cluster-management/api v0.0.0-20200610161514-939cead3902c
	github.com/operator-framework/operator-sdk v0.18.1
	github.com/spf13/pflag v1.0.5
	golang.org/x/sys v0.0.0-20210326220804-49726bf1d181 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	google.golang.org/protobuf v1.25.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/api v0.18.3
	k8s.io/apimachinery v0.18.3
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.6.0
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible // Required by OLM
	golang.org/x/text => golang.org/x/text v0.3.3 // CVE-2020-14040
	howett.net/plist => github.com/DHowett/go-plist v0.0.0-20181124034731-591f970eefbb
	k8s.io/client-go => k8s.io/client-go v0.18.3 // Required by prometheus-operator
)
