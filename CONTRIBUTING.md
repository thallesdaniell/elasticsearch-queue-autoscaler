# Contributing

Use Go 1.24.7, Helm 3 and an available container runtime. Run `make verify` before a PR.
Keep the decision engine pure. Add regression tests for every safety change. Never replace ECK reconciliation with direct StatefulSet operations.
API is v1alpha1 and may change. Breaking behavior needs an ADR and migration notes. Scale down requires its own design and failure-injection tests.
The scaffold follows Kubebuilder go/v4 layout and uses controller-gen markers. Regenerate CRDs and DeepCopy with `make generate manifests`. The module path is github.com/thallesdaniell/elasticsearch-queue-autoscaler.
