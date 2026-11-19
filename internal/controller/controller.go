package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	api "github.com/thallesdaniell/elasticsearch-queue-autoscaler/api/v1alpha1"
	"github.com/thallesdaniell/elasticsearch-queue-autoscaler/internal/engine"
	"github.com/thallesdaniell/elasticsearch-queue-autoscaler/internal/safety"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"strconv"
	"strings"
	"time"
)

const OwnersAnnotation = "queue-autoscaler.io/owners"
const PendingNodeSetAnnotation = "queue-autoscaler.io/pending-node-set"
const OwnerAnnotation = "queue-autoscaler.io/owner"
const NodeSetAnnotation = "queue-autoscaler.io/node-set"
const CooldownUntilAnnotation = "queue-autoscaler.io/cooldown-until"
const LastScaleAnnotation = "queue-autoscaler.io/last-scale"
const PendingAnnotation = "queue-autoscaler.io/pending-count"

var ECKGVK = schema.GroupVersionKind{Group: "elasticsearch.k8s.elastic.co", Version: "v1", Kind: "Elasticsearch"}
var decisions = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "eqa_decisions_total", Help: "Reconcile decisions by bounded outcome."}, []string{"reason"})
var replicas = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "eqa_replicas", Help: "Current and desired NodeSet replicas."}, []string{"namespace", "autoscaler", "kind"})

var lastReconcile = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "eqa_last_reconcile_timestamp_seconds", Help: "Last decision timestamp per autoscaler."}, []string{"namespace", "autoscaler"})
var pendingSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "eqa_pending_seconds", Help: "Time continuously waiting for cluster convergence; zero when converged."}, []string{"namespace", "autoscaler"})

func init() { metrics.Registry.MustRegister(decisions, replicas, lastReconcile, pendingSeconds) }

type Reconciler struct {
	client.Client
	Reader   client.Reader // Uncached reads are mandatory for the target and readiness checks.
	Safety   safety.Checker
	Recorder record.EventRecorder
	Now      func() time.Time
}

func (r *Reconciler) SetupWithManager(m ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(m).For(&api.ElasticsearchQueueAutoscaler{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).WithOptions(crcontroller.Options{MaxConcurrentReconciles: 1}).Complete(r)
}
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	var a api.ElasticsearchQueueAutoscaler
	if e := r.Reader.Get(ctx, req.NamespacedName, &a); e != nil {
		return ctrl.Result{}, client.IgnoreNotFound(e)
	}
	if !a.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	base := a.DeepCopy()
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	poll := time.Duration(a.Spec.Scaling.PollSeconds) * time.Second
	if poll < 15*time.Second {
		poll = 30 * time.Second
	}
	finish := func(reason, message string, ready bool, reset bool) (ctrl.Result, error) {
		if reset {
			a.Status.CandidateSince = nil
			a.Status.CandidateReplicas = 0
		}
		a.Status.ObservedGeneration = a.Generation
		a.Status.LastDecision = reason + ": " + message
		state := metav1.ConditionFalse
		if ready {
			state = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&a.Status.Conditions, metav1.Condition{Type: "Ready", Status: state, Reason: reason, Message: message, ObservedGeneration: a.Generation, LastTransitionTime: metav1.NewTime(now)})
		decisions.WithLabelValues(reason).Inc()
		replicas.WithLabelValues(a.Namespace, a.Name, "current").Set(float64(a.Status.CurrentReplicas))
		replicas.WithLabelValues(a.Namespace, a.Name, "desired").Set(float64(a.Status.DesiredReplicas))
		replicas.WithLabelValues(a.Namespace, a.Name, "requested").Set(float64(a.Spec.RequestedReplicas))
		replicas.WithLabelValues(a.Namespace, a.Name, "observed").Set(float64(a.Status.Replicas))
		replicas.WithLabelValues(a.Namespace, a.Name, "ready").Set(float64(a.Status.ReadyReplicas))
		lastReconcile.WithLabelValues(a.Namespace, a.Name).Set(float64(now.Unix()))
		elapsed := float64(0)
		if a.Status.PendingSince != nil {
			elapsed = now.Sub(a.Status.PendingSince.Time).Seconds()
			if elapsed < 0 {
				elapsed = 0
			}
		}
		pendingSeconds.WithLabelValues(a.Namespace, a.Name).Set(elapsed)
		if r.Recorder != nil && base.Status.LastDecision != a.Status.LastDecision {
			kind := corev1.EventTypeNormal
			if !ready {
				kind = corev1.EventTypeWarning
			}
			r.Recorder.Event(&a, kind, reason, message)
		}
		ctrl.LoggerFrom(ctx).Info("scaling decision", "reason", reason, "message", message, "current", a.Status.CurrentReplicas, "desired", a.Status.DesiredReplicas, "mode", a.Spec.Mode)
		e := r.Status().Patch(ctx, &a, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
		return ctrl.Result{RequeueAfter: poll}, e
	}
	pending := func(message string) (ctrl.Result, error) {
		if a.Status.PendingSince == nil {
			t := metav1.NewTime(now)
			a.Status.PendingSince = &t
		}
		timeout := a.Spec.Scaling.ConvergenceTimeoutSeconds
		if timeout == 0 {
			timeout = 2700
		}
		if now.Sub(a.Status.PendingSince.Time) >= time.Duration(timeout)*time.Second {
			return finish("ScaleStalled", message+"; convergence deadline exceeded, intervention required; no rollback", false, true)
		}
		return finish("PendingScale", message, false, true)
	}
	if e := engine.Validate(a.Spec); e != nil {
		return finish("InvalidSpec", e.Error(), false, true)
	}
	es := &unstructured.Unstructured{}
	es.SetGroupVersionKind(ECKGVK)
	if e := r.Reader.Get(ctx, types.NamespacedName{Namespace: a.Namespace, Name: a.Spec.TargetRef.Name}, es); e != nil {
		return finish("TargetUnavailable", e.Error(), false, true)
	}
	annotations := es.GetAnnotations()
	if !es.GetDeletionTimestamp().IsZero() || !Authorized(annotations, a.Name, a.Spec.TargetRef.NodeSet) {
		return finish("Unauthorized", "target must explicitly authorize this autoscaler and NodeSet", false, true)
	}
	index, current, total, e := Target(es, a.Spec.TargetRef.NodeSet)
	if e != nil {
		return finish("InvalidTarget", e.Error(), false, true)
	}
	selector := labels.Set{
		"elasticsearch.k8s.elastic.co/cluster-name":     es.GetName(),
		"elasticsearch.k8s.elastic.co/statefulset-name": es.GetName() + "-es-" + a.Spec.TargetRef.NodeSet,
	}
	a.Status.Selector = selector.String()
	var selected corev1.PodList
	if e := r.Reader.List(ctx, &selected, client.InNamespace(a.Namespace), client.MatchingLabels(selector)); e != nil {
		return finish("TargetUnavailable", e.Error(), false, true)
	}
	a.Status.Replicas, a.Status.ReadyReplicas = 0, 0
	for _, pod := range selected.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		a.Status.Replicas++
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				a.Status.ReadyReplicas++
				break
			}
		}
	}
	a.Status.CurrentReplicas = current
	a.Status.DesiredReplicas = current
	// The operation marker is cluster-wide; compare it against its own NodeSet.
	if v := annotations[PendingAnnotation]; v != "" {
		count, err := strconv.ParseInt(v, 10, 32)
		pendingSet := annotations[PendingNodeSetAnnotation]
		if pendingSet == "" {
			pendingSet = annotations[NodeSetAnnotation]
		} // v0.1/v0.2 legacy marker
		_, actual, _, targetErr := Target(es, pendingSet)
		if err != nil || count < 1 || targetErr != nil || int64(actual) < count {
			return pending("previous target not reached or operation marker invalid; investigate GitOps or manual changes")
		}
	}
	if e = r.stable(ctx, es, total); e != nil {
		return pending(e.Error())
	}
	a.Status.PendingSince = nil
	if a.Spec.Safety.RequireDistinctNodes {
		hosts := map[string]bool{}
		for _, pod := range selected.Items {
			if pod.Spec.NodeName == "" || hosts[pod.Spec.NodeName] {
				return finish("SafetyBlocked", "selected NodeSet requires distinct Kubernetes nodes", false, true)
			}
			hosts[pod.Spec.NodeName] = true
		}
	}
	endpoint := fmt.Sprintf("https://%s-es-http.%s.svc:9200", es.GetName(), es.GetNamespace())
	if e = r.Safety.Check(ctx, endpoint, a.Spec.Safety, total); e != nil {
		return finish("SafetyBlocked", e.Error(), false, true)
	}
	floor, e := engine.ScheduleFloor(now, a.Spec.Sources.Schedules)
	if e != nil {
		return finish("InvalidSchedule", e.Error(), false, true)
	}
	desired := engine.Desired(current, a.Spec.RequestedReplicas, floor, a.Spec.Scaling)
	a.Status.DesiredReplicas = desired
	if desired <= current {
		return finish("NoChange", "scale down disabled; current capacity retained", true, true)
	}
	if v := annotations[CooldownUntilAnnotation]; v != "" {
		until, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return finish("SafetyBlocked", "invalid cooldown-until timestamp", false, true)
		}
		if now.Before(until) {
			return finish("Cooldown", "waiting for previous writer's cluster cooldown", true, true)
		}
	}
	if v := annotations[LastScaleAnnotation]; v != "" {
		last, e := time.Parse(time.RFC3339Nano, v)
		if e != nil || last.After(now) {
			return finish("SafetyBlocked", "invalid last-scale timestamp", false, true)
		}
		if now.Sub(last) < time.Duration(a.Spec.Scaling.CooldownSeconds)*time.Second {
			return finish("Cooldown", "waiting for scale-up cooldown", true, true)
		}
	}
	// Hold a persistent lower-bound recommendation across the entire stabilization window.
	if a.Status.CandidateSince == nil || a.Status.CandidatePolicyHash != policyHash(a.Spec) || desired < a.Status.CandidateReplicas {
		t := metav1.NewTime(now)
		a.Status.CandidateSince = &t
		a.Status.CandidateReplicas = desired
		a.Status.CandidatePolicyHash = policyHash(a.Spec)
	}
	if now.Sub(a.Status.CandidateSince.Time) < time.Duration(a.Spec.Scaling.StabilizationSeconds)*time.Second {
		return finish("Stabilizing", "waiting for sustained demand", true, false)
	}
	desired = a.Status.CandidateReplicas
	a.Status.DesiredReplicas = desired
	message := fmt.Sprintf("NodeSet %s: %d -> %d (kedaRequested=%d scheduleFloor=%d)", a.Spec.TargetRef.NodeSet, current, desired, a.Spec.RequestedReplicas, floor)
	if a.Spec.Mode == "Audit" || a.Spec.Mode == "" {
		return finish("Audit", message, true, false)
	}
	// Re-read the policy immediately before mutation; changed/deleted policy cannot authorize this decision.
	var fresh api.ElasticsearchQueueAutoscaler
	if e = r.Reader.Get(ctx, req.NamespacedName, &fresh); e != nil {
		return ctrl.Result{}, e
	}
	if fresh.UID != a.UID || fresh.Generation != a.Generation || !fresh.DeletionTimestamp.IsZero() {
		return ctrl.Result{Requeue: true}, nil
	}
	patch, e := ScalePatch(es, index, current, desired, now, a.Spec.Scaling.CooldownSeconds)
	if e != nil {
		return ctrl.Result{}, e
	}
	opts := []client.PatchOption{client.FieldOwner("elasticsearch-queue-autoscaler")}
	if a.Spec.Mode == "DryRun" {
		opts = append(opts, client.DryRunAll)
	}
	if e = r.Patch(ctx, es, client.RawPatch(types.JSONPatchType, patch), opts...); e != nil {
		return finish("PatchRejected", e.Error(), false, true)
	}
	if a.Spec.Mode == "DryRun" {
		return finish("DryRun", message, true, false)
	}
	t := metav1.NewTime(now)
	a.Status.LastScaleTime = &t
	return finish("ScaledUp", message, true, true)
}

// Target rejects implicit/default roles and master-eligible NodeSets in this MVP.
func Target(es *unstructured.Unstructured, name string) (int, int32, int64, error) {
	sets, ok, e := unstructured.NestedSlice(es.Object, "spec", "nodeSets")
	if e != nil || !ok {
		return 0, 0, 0, fmt.Errorf("missing nodeSets")
	}
	index := -1
	var current int32
	var total int64
	for i, s := range sets {
		n, ok := s.(map[string]any)
		if !ok {
			return 0, 0, 0, fmt.Errorf("invalid nodeSet")
		}
		count, ok, e := unstructured.NestedInt64(n, "count")
		if e != nil || !ok || count < 0 || count > 10000 {
			return 0, 0, 0, fmt.Errorf("invalid node count")
		}
		total += count
		if n["name"] != name {
			continue
		}
		if index >= 0 {
			return 0, 0, 0, fmt.Errorf("duplicate NodeSet")
		}
		index = i
		current = int32(count)
		config, ok := n["config"].(map[string]any)
		if !ok {
			return 0, 0, 0, fmt.Errorf("explicit node.roles required")
		}
		roles, ok := config["node.roles"].([]any)
		if !ok {
			return 0, 0, 0, fmt.Errorf("explicit node.roles required")
		}
		data := false
		for _, role := range roles {
			v, _ := role.(string)
			if v == "master" {
				return 0, 0, 0, fmt.Errorf("master-eligible NodeSet is forbidden")
			}
			if v == "data" || strings.HasPrefix(v, "data_") {
				data = true
			}
		}
		if !data {
			return 0, 0, 0, fmt.Errorf("target must have a data role")
		}
	}
	if index < 0 {
		return 0, 0, 0, fmt.Errorf("NodeSet not found")
	}
	return index, current, total, nil
}
func (r *Reconciler) stable(ctx context.Context, es *unstructured.Unstructured, total int64) error {
	observed, ok, _ := unstructured.NestedInt64(es.Object, "status", "observedGeneration")
	if !ok || observed != es.GetGeneration() {
		return fmt.Errorf("ECK has not observed target generation")
	}
	phase, _, _ := unstructured.NestedString(es.Object, "status", "phase")
	if phase != "Ready" {
		return fmt.Errorf("ECK phase is %s", phase)
	}
	// Pod count and readiness are checked independently of ECK's eventually consistent status.
	var pods corev1.PodList
	if e := r.Reader.List(ctx, &pods, client.InNamespace(es.GetNamespace()), client.MatchingLabels{"elasticsearch.k8s.elastic.co/cluster-name": es.GetName()}); e != nil {
		return e
	}
	if int64(len(pods.Items)) != total {
		return fmt.Errorf("waiting for pods: %d/%d", len(pods.Items), total)
	}
	counts := map[string]int64{}
	for _, p := range pods.Items {
		ready := false
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if !ready || !p.DeletionTimestamp.IsZero() {
			return fmt.Errorf("pod not ready or terminating")
		}
		counts[p.Labels["elasticsearch.k8s.elastic.co/statefulset-name"]]++
	}
	sets, _, _ := unstructured.NestedSlice(es.Object, "spec", "nodeSets")
	for _, s := range sets {
		n := s.(map[string]any)
		count, _, _ := unstructured.NestedInt64(n, "count")
		if counts[es.GetName()+"-es-"+n["name"].(string)] != count {
			return fmt.Errorf("NodeSet pods not converged")
		}
	}
	return nil
}

// ScalePatch changes exactly one count plus recovery metadata. resourceVersion is a CAS guard.
func ScalePatch(es *unstructured.Unstructured, index int, current, desired int32, now time.Time, cooldownSeconds int32) ([]byte, error) {
	if desired <= current || cooldownSeconds < 60 {
		return nil, fmt.Errorf("scale down, no-op or invalid cooldown forbidden")
	}
	sets, _, _ := unstructured.NestedSlice(es.Object, "spec", "nodeSets")
	if index < 0 || index >= len(sets) {
		return nil, fmt.Errorf("invalid nodeSet index")
	}
	n := sets[index].(map[string]any)
	op := []map[string]any{
		{"op": "test", "path": "/metadata/resourceVersion", "value": es.GetResourceVersion()},
		{"op": "test", "path": fmt.Sprintf("/spec/nodeSets/%d/name", index), "value": n["name"]},
		{"op": "test", "path": fmt.Sprintf("/spec/nodeSets/%d/count", index), "value": current},
		{"op": "replace", "path": fmt.Sprintf("/spec/nodeSets/%d/count", index), "value": desired},
		{"op": "add", "path": "/metadata/annotations/queue-autoscaler.io~1last-scale", "value": now.UTC().Format(time.RFC3339Nano)},
		{"op": "add", "path": "/metadata/annotations/queue-autoscaler.io~1pending-count", "value": strconv.Itoa(int(desired))},
		{"op": "add", "path": "/metadata/annotations/queue-autoscaler.io~1pending-node-set", "value": n["name"]},
		{"op": "add", "path": "/metadata/annotations/queue-autoscaler.io~1cooldown-until", "value": now.Add(time.Duration(cooldownSeconds) * time.Second).UTC().Format(time.RFC3339Nano)},
	}
	return json.Marshal(op)
}

// Demand changes do not restart stabilization. Configuration/mode changes do.
func policyHash(spec api.ElasticsearchQueueAutoscalerSpec) string {
	spec.RequestedReplicas = 0
	encoded, _ := json.Marshal(spec)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

// Owners maps each authorized NodeSet to exactly one CR name in this namespace.
// If present, this map is authoritative; malformed maps never fall back to legacy authorization.
func Authorized(annotations map[string]string, autoscaler, nodeSet string) bool {
	if raw, exists := annotations[OwnersAnnotation]; exists {
		owners := map[string]string{}
		if json.Unmarshal([]byte(raw), &owners) != nil {
			return false
		}
		return autoscaler != "" && owners[nodeSet] == autoscaler
	}
	return annotations[OwnerAnnotation] == autoscaler && annotations[NodeSetAnnotation] == nodeSet && autoscaler != ""
}
