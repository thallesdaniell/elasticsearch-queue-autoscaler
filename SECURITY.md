# Security

This alpha operator can increase infrastructure cost and trigger shard movement. Start in Audit.
Report security problems privately to the repository maintainers; configure GitHub private vulnerability reporting before publishing. Do not disclose credentials in issues.

The controller has no Secret read API permissions. API key and trusted CA are mounted. Use an Elasticsearch API key with cluster `monitor` and index `monitor` for the `/_cat/shards` endpoint, without document read/write or settings mutation privileges. Shard inventory must cover all indices, including restricted indices when present (`allow_restricted_indices: true` for this read-only monitoring role). A filtered inventory is insufficient for a cluster-wide shard limit. Do not use the elastic superuser.

Kubernetes authorization is namespaced and Elasticsearch patches are restricted by resourceNames. Kubernetes RBAC cannot restrict individual JSON fields; code guards and protected target annotations provide NodeSet scoping. Anyone able to modify the operator, its RBAC or target annotations is trusted. For an adversarial multi-tenant boundary, add admission policy validating the service account's changed paths.

No direct StatefulSet/PVC writes. Automatic scale down is absent. ResourceVersion compare-and-swap rejects stale changes. Leader election and one authorized owner per NodeSet with cluster-wide convergence checks serialize changes; manual operators and GitOps must respect ownership too.

Prometheus endpoint is plain HTTP, internal only, with a chart NetworkPolicy. This requires a CNI enforcing NetworkPolicy. No ingress/LoadBalancer is created. Configure monitoring namespace access explicitly.

Dependency versions form a reproducible baseline, not a claim of current security patch coverage. Scan and update dependencies and pin action/container digests before a production release.

KEDA/HPA is a trusted demand writer to the CR scale subresource, not to Elasticsearch. Restrict scale writes to the intended resource and identities. Requested replicas are persistent intent, not a freshness proof; stop new ECK writes using Audit when a source incident requires a pause. The operator does not require Google credentials.
