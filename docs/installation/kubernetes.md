# Kubernetes Installation

A [Helm](https://helm.sh) chart is available to install Codes on a Kubernetes cluster.

Add the Codes Helm repository:

```bash
helm repo add codes https://codes.devemm.rw
helm repo update
helm install opengist codes/codes
```

The chart retains the historical `opengist` release and Kubernetes resource
names for in-place upgrades and persistent volume compatibility. When upgrading
an existing installation, keep its current Helm release name.
