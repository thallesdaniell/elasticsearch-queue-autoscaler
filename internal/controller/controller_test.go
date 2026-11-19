package controller

import (
	"context"
	"encoding/json"
	"fmt"
	api "github.com/thallesdaniell/elasticsearch-queue-autoscaler/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

type safe struct{}

func (safe) Check(context.Context, string, api.Safety, int64) error { return nil }
func fixture(t *testing.T) (*Reconciler, *api.ElasticsearchQueueAutoscaler, *unstructured.Unstructured) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	a := &api.ElasticsearchQueueAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "scale", Namespace: "test", Generation: 1}, Spec: api.ElasticsearchQueueAutoscalerSpec{TargetRef: api.TargetRef{Name: "es", NodeSet: "data"}, Mode: "Auto", Provider: "KEDA", RequestedReplicas: 6, Scaling: api.Scaling{MinReplicas: 1, MaxReplicas: 10, MaxStep: 2, PollSeconds: 30, CooldownSeconds: 60}, Safety: api.Safety{RequiredHealth: "green", MaxDiskUsedPercent: 90, MaxShardsPerNode: 1000, BlockScaleDown: true}}}
	es := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "elasticsearch.k8s.elastic.co/v1", "kind": "Elasticsearch", "metadata": map[string]any{"name": "es", "namespace": "test", "generation": int64(1), "annotations": map[string]any{OwnerAnnotation: "scale", NodeSetAnnotation: "data"}}, "spec": map[string]any{"nodeSets": []any{map[string]any{"name": "masters", "count": int64(1), "config": map[string]any{"node.roles": []any{"master"}}}, map[string]any{"name": "data", "count": int64(2), "config": map[string]any{"node.roles": []any{"data_hot"}}}}}, "status": map[string]any{"phase": "Ready", "observedGeneration": int64(1)}}}
	objects := []client.Object{a, es}
	for _, name := range []string{"masters-0", "data-0", "data-1"} {
		set := "data"
		if name == "masters-0" {
			set = "masters"
		}
		objects = append(objects, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test", Labels: map[string]string{"elasticsearch.k8s.elastic.co/cluster-name": "es", "elasticsearch.k8s.elastic.co/statefulset-name": "es-es-" + set}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}})
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(a).WithObjects(objects...).Build()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	return &Reconciler{Client: c, Reader: c, Safety: safe{}, Now: func() time.Time { return now }}, a, es
}
func TestReconcileSafety(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*api.ElasticsearchQueueAutoscaler, *unstructured.Unstructured)
		want   int32
	}{
		{"auto", func(*api.ElasticsearchQueueAutoscaler, *unstructured.Unstructured) {}, 4},
		{"audit", func(a *api.ElasticsearchQueueAutoscaler, _ *unstructured.Unstructured) { a.Spec.Mode = "Audit" }, 2},
		{"unauthorized", func(_ *api.ElasticsearchQueueAutoscaler, e *unstructured.Unstructured) {
			e.SetAnnotations(map[string]string{})
		}, 2},
		{"pending", func(_ *api.ElasticsearchQueueAutoscaler, e *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(e.Object, "ApplyingChanges", "status", "phase")
		}, 2},
		{"unobserved", func(_ *api.ElasticsearchQueueAutoscaler, e *unstructured.Unstructured) { e.SetGeneration(2) }, 2},
		{"stabilizing", func(a *api.ElasticsearchQueueAutoscaler, _ *unstructured.Unstructured) {
			a.Spec.Scaling.StabilizationSeconds = 60
		}, 2},
		{"no scale down", func(a *api.ElasticsearchQueueAutoscaler, _ *unstructured.Unstructured) {
			a.Spec.Scaling.MaxReplicas = 1
		}, 2},
		{"gitops reverted", func(_ *api.ElasticsearchQueueAutoscaler, e *unstructured.Unstructured) {
			m := e.GetAnnotations()
			m[PendingAnnotation] = "4"
			e.SetAnnotations(m)
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a, es := fixture(t)
			tc.mutate(a, es)
			ctx := context.Background()
			if e := r.Update(ctx, a); e != nil {
				t.Fatal(e)
			}
			if e := r.Update(ctx, es); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 2; i++ {
				if _, e := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: a.Name, Namespace: a.Namespace}}); e != nil {
					t.Fatal(e)
				}
			}
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(ECKGVK)
			if e := r.Get(ctx, client.ObjectKeyFromObject(es), got); e != nil {
				t.Fatal(e)
			}
			_, count, _, e := Target(got, "data")
			if e != nil || count != tc.want {
				t.Fatalf("count=%d want=%d err=%v", count, tc.want, e)
			}
			sets, _, _ := unstructured.NestedSlice(got.Object, "spec", "nodeSets")
			if sets[0].(map[string]any)["count"] != int64(1) {
				t.Fatal("changed unauthorized NodeSet")
			}
		})
	}
}
func TestPatchGuards(t *testing.T) {
	_, _, es := fixture(t)
	es.SetResourceVersion("42")
	p, e := ScalePatch(es, 1, 2, 4, time.Now(), 60)
	if e != nil {
		t.Fatal(e)
	}
	var ops []map[string]any
	if e = json.Unmarshal(p, &ops); e != nil {
		t.Fatal(e)
	}
	if ops[0]["op"] != "test" || ops[0]["value"] != "42" || ops[3]["path"] != "/spec/nodeSets/1/count" {
		t.Fatal(string(p))
	}
	if _, e = ScalePatch(es, 1, 2, 1, time.Now(), 60); e == nil {
		t.Fatal("accepted downscale")
	}
}

func TestKEDADemandAndObservedScale(t *testing.T) {
	for _, requested := range []int32{0, 1, 2, 8} {
		r, a, es := fixture(t)
		a.Spec.RequestedReplicas = requested
		if e := r.Update(context.Background(), a); e != nil {
			t.Fatal(e)
		}
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}
		if _, e := r.Reconcile(context.Background(), req); e != nil {
			t.Fatal(e)
		}
		if e := r.Get(context.Background(), client.ObjectKeyFromObject(a), a); e != nil {
			t.Fatal(e)
		}
		if a.Status.Replicas != 2 || a.Status.ReadyReplicas != 2 || a.Status.Selector != "elasticsearch.k8s.elastic.co/cluster-name=es,elasticsearch.k8s.elastic.co/statefulset-name=es-es-data" {
			t.Fatalf("incorrect scale status: %+v", a.Status)
		}
		if a.Spec.RequestedReplicas != requested {
			t.Fatal("controller overwrote KEDA demand")
		}
		if e := r.Get(context.Background(), client.ObjectKeyFromObject(es), es); e != nil {
			t.Fatal(e)
		}
		_, count, _, _ := Target(es, "data")
		want := int32(2)
		if requested > 2 {
			want = 4
		}
		if count != want {
			t.Fatalf("requested %d count %d want %d", requested, count, want)
		}
	}
}

func TestKEDAUpdatesDoNotStarveStabilization(t *testing.T) {
	r, a, es := fixture(t)
	ctx := context.Background()
	a.Spec.Scaling.StabilizationSeconds = 60
	if e := r.Update(ctx, a); e != nil {
		t.Fatal(e)
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}
	if _, e := r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	}
	if e := r.Get(ctx, client.ObjectKeyFromObject(a), a); e != nil {
		t.Fatal(e)
	}
	started := a.Status.CandidateSince.Time
	a.Spec.RequestedReplicas = 8
	a.Generation++
	if e := r.Update(ctx, a); e != nil {
		t.Fatal(e)
	}
	r.Now = func() time.Time { return started.Add(61 * time.Second) }
	if _, e := r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	}
	if e := r.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, count, _, _ := Target(es, "data")
	if count != 4 {
		t.Fatalf("demand update reset stabilization, count=%d", count)
	}
}

func TestPolicyChangeRestartsStabilization(t *testing.T) {
	r, a, es := fixture(t)
	ctx := context.Background()
	a.Spec.Scaling.StabilizationSeconds = 60
	if e := r.Update(ctx, a); e != nil {
		t.Fatal(e)
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}
	if _, e := r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	}
	if e := r.Get(ctx, client.ObjectKeyFromObject(a), a); e != nil {
		t.Fatal(e)
	}
	started := a.Status.CandidateSince.Time
	a.Spec.Safety.MaxDiskUsedPercent = 80
	a.Generation++
	if e := r.Update(ctx, a); e != nil {
		t.Fatal(e)
	}
	r.Now = func() time.Time { return started.Add(61 * time.Second) }
	if _, e := r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	}
	if e := r.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, count, _, _ := Target(es, "data")
	if count != 2 {
		t.Fatalf("policy change reused old stabilization, count=%d", count)
	}
}

func TestOwnersMap(t *testing.T) {
	for _, tc := range []struct {
		raw, name, set string
		want           bool
	}{
		{`{"data":"scale","other":"scale-other"}`, "scale", "data", true},
		{`{"data":"scale","other":"scale-other"}`, "scale", "other", false},
		{`{"data":"scale"}`, "intruder", "data", false},
		{`{broken`, "scale", "data", false},
		{`{}`, "scale", "data", false},
	} {
		annotations := map[string]string{OwnersAnnotation: tc.raw, OwnerAnnotation: "scale", NodeSetAnnotation: "data"}
		if got := Authorized(annotations, tc.name, tc.set); got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
}

func TestMultipleNodeSetsWaitForClusterConvergence(t *testing.T) {
	r, a, es := fixture(t)
	ctx := context.Background()
	a.Spec.Scaling.CooldownSeconds = 600
	if e := r.Update(ctx, a); e != nil {
		t.Fatal(e)
	}
	other := a.DeepCopy()
	other.Name = "scale-other"
	other.ResourceVersion = ""
	other.Spec.TargetRef.NodeSet = "other"
	other.Spec.Scaling.CooldownSeconds = 60
	if e := r.Create(ctx, other); e != nil {
		t.Fatal(e)
	}
	sets, _, _ := unstructured.NestedSlice(es.Object, "spec", "nodeSets")
	sets = append(sets, map[string]any{"name": "other", "count": int64(1), "config": map[string]any{"node.roles": []any{"data_hot"}}})
	_ = unstructured.SetNestedSlice(es.Object, sets, "spec", "nodeSets")
	es.SetAnnotations(map[string]string{OwnersAnnotation: `{"data":"scale","other":"scale-other"}`})
	if e := r.Update(ctx, es); e != nil {
		t.Fatal(e)
	}
	addPod := func(name, set string) {
		t.Helper()
		if e := r.Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.Namespace, Labels: map[string]string{"elasticsearch.k8s.elastic.co/cluster-name": "es", "elasticsearch.k8s.elastic.co/statefulset-name": "es-es-" + set}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}); e != nil {
			t.Fatal(e)
		}
	}
	addPod("other-0", "other")
	reconcile := func(obj client.Object) {
		t.Helper()
		if _, e := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}); e != nil {
			t.Fatal(e)
		}
	}
	reconcile(a)
	reconcile(other)
	if e := r.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, data, _, _ := Target(es, "data")
	_, second, _, _ := Target(es, "other")
	if data != 4 || second != 1 || es.GetAnnotations()[PendingNodeSetAnnotation] != "data" {
		t.Fatalf("overlapping scaling: data=%d other=%d", data, second)
	}
	// Restarted controller reads operation marker for data, NOT against other's count=1.
	addPod("data-2", "data")
	addPod("data-3", "data")
	now := r.Now().Add(61 * time.Second)
	r = &Reconciler{Client: r.Client, Reader: r.Reader, Safety: safe{}, Now: func() time.Time { return now }}
	reconcile(other)
	if e := r.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, blockedCount, _, _ := Target(es, "other")
	if blockedCount != 1 {
		t.Fatal("second writer bypassed first writer's cooldown")
	}
	now = now.Add(540 * time.Second)
	reconcile(other)
	if e := r.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, data, _, _ = Target(es, "data")
	_, second, _, _ = Target(es, "other")
	if data != 4 || second != 3 || es.GetAnnotations()[PendingNodeSetAnnotation] != "other" {
		t.Fatalf("second NodeSet blocked after convergence: data=%d other=%d", data, second)
	}
}

func TestStallReportsWithoutRollback(t *testing.T) {
	r, a, es := fixture(t)
	ctx := context.Background()
	a.Spec.Scaling.ConvergenceTimeoutSeconds = 60
	if e := r.Update(ctx, a); e != nil {
		t.Fatal(e)
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}
	if _, e := r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	} // applies count=4
	if _, e := r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	} // waiting for pods
	now := r.Now().Add(61 * time.Second)
	r = &Reconciler{Client: r.Client, Reader: r.Reader, Safety: safe{}, Now: func() time.Time { return now }}
	if _, e := r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	}
	if e := r.Get(ctx, client.ObjectKeyFromObject(a), a); e != nil {
		t.Fatal(e)
	}
	if a.Status.Conditions[0].Reason != "ScaleStalled" {
		t.Fatalf("missing stall: %+v", a.Status)
	}
	if e := r.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, count, _, _ := Target(es, "data")
	if count != 4 {
		t.Fatalf("rolled back count=%d", count)
	}
}

func TestRequireDistinctNodes(t *testing.T) {
	for _, distinct := range []bool{false, true} {
		r, a, es := fixture(t)
		ctx := context.Background()
		a.Spec.Safety.RequireDistinctNodes = true
		if e := r.Update(ctx, a); e != nil {
			t.Fatal(e)
		}
		for i, name := range []string{"data-0", "data-1"} {
			pod := &corev1.Pod{}
			if e := r.Get(ctx, types.NamespacedName{Namespace: a.Namespace, Name: name}, pod); e != nil {
				t.Fatal(e)
			}
			pod.Spec.NodeName = "host-0"
			if distinct {
				pod.Spec.NodeName = fmt.Sprintf("host-%d", i)
			}
			if e := r.Update(ctx, pod); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); e != nil {
			t.Fatal(e)
		}
		if e := r.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
			t.Fatal(e)
		}
		_, count, _, _ := Target(es, "data")
		want := int32(2)
		if distinct {
			want = 4
		}
		if count != want {
			t.Fatalf("distinct=%v count=%d", distinct, count)
		}
	}
}
