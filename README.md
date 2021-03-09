[comment]: # ( Copyright Contributors to the Open Cluster Management project )

# Governance Policy Propagator
Red Hat Advance Cluster Management Governance - Policy Propagator

## How it works

This operator watches for following changes to trigger reconcile


1. policies changes in non-cluster namespaces

    a. policies in non-cluster namespaces triggers self reconcile

    b. policies in cluster namespaces triggers root policy reconcile
2. placementbinding changes
3. placementrule changes

Every reconcile does following things:

1. Create/update/delete replicated policy in cluster namespace based on pb/plr results
2. Create/update/delete policy status to show aggregated cluster compliance results

## Run
```
export WATCH_NAMESPACE=""
operator-sdk run --local
```

## Run e2e test
Make sure you have [kind](https://github.com/kubernetes-sigs/kind) and [ginkgo](https://github.com/onsi/ginkgo) installed. 
```
make kind-bootstrap-cluster
make e2e-test
```
To cleanup
```
make kind-delete-cluster
```

<!---
Date: Nov/10/2020
-->
