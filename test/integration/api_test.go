//go:build integration

package integration

import (
	"context"
	api "github.com/thallesdaniell/elasticsearch-queue-autoscaler/api/v1alpha1"
	"github.com/thallesdaniell/elasticsearch-queue-autoscaler/internal/controller"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"path/filepath"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type healthy struct{}

func (healthy) Check(context.Context, string, api.Safety, int64) error { return nil }
func TestAPIServer(t *testing.T) {
	ctx := context.Background()
	preserve := true
	// Minimal ECK contract fixture. This test does not run the ECK operator.
	eck := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "elasticsearches.elasticsearch.k8s.elastic.co"}, Spec: apiextensionsv1.CustomResourceDefinitionSpec{Group: "elasticsearch.k8s.elastic.co", Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: "elasticsearches", Singular: "elasticsearch", Kind: "Elasticsearch"}, Scope: apiextensionsv1.NamespaceScoped, Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{Name: "v1", Served: true, Storage: true, Subresources: &apiextensionsv1.CustomResourceSubresources{Status: &apiextensionsv1.CustomResourceSubresourceStatus{}}, Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{"spec": {Type: "object", XPreserveUnknownFields: &preserve}, "status": {Type: "object", XPreserveUnknownFields: &preserve}}}}}}}}
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, CRDs: []*apiextensionsv1.CustomResourceDefinition{eck}, ErrorIfCRDPathMissing: true}
	env.ControlPlane.GetAPIServer().Configure().Set("advertise-address", "127.0.0.1")
	cfg, e := env.Start()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := env.Stop(); e != nil {
			t.Error(e)
		}
	}()
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = autoscalingv1.AddToScheme(scheme)
	c, e := client.New(cfg, client.Options{Scheme: scheme})
	if e != nil {
		t.Fatal(e)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	if e = c.Create(ctx, ns); e != nil {
		t.Fatal(e)
	}
	a := &api.ElasticsearchQueueAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "scale", Namespace: "test"}, Spec: api.ElasticsearchQueueAutoscalerSpec{TargetRef: api.TargetRef{Name: "es", NodeSet: "data"}, Mode: "DryRun", Provider: "KEDA", RequestedReplicas: 2, Scaling: api.Scaling{MinReplicas: 1, MaxReplicas: 10, MaxStep: 2, PollSeconds: 30, CooldownSeconds: 60}, Safety: api.Safety{BlockScaleDown: true, RequiredHealth: "green", MaxDiskUsedPercent: 90, MaxShardsPerNode: 1000}}}
	if e = c.Create(ctx, a); e != nil {
		t.Fatal(e)
	}
	// Exercise the actual CRD /scale endpoint, the same contract KEDA/HPA uses.
	scale := &autoscalingv1.Scale{}
	if e = c.SubResource("scale").Get(ctx, a, scale); e != nil {
		t.Fatal(e)
	}
	if scale.Spec.Replicas != 2 {
		t.Fatalf("initial scale: %+v", scale)
	}
	staleScale := scale.DeepCopy()
	scale.Spec.Replicas = 8
	if e = c.SubResource("scale").Update(ctx, a, client.WithSubResourceBody(scale)); e != nil {
		t.Fatal(e)
	}
	staleScale.Spec.Replicas = 9
	if e = c.SubResource("scale").Update(ctx, a, client.WithSubResourceBody(staleScale)); e == nil {
		t.Fatal("stale /scale update accepted")
	}
	if e = c.Get(ctx, client.ObjectKeyFromObject(a), a); e != nil {
		t.Fatal(e)
	}
	if a.Spec.RequestedReplicas != 8 || a.Spec.TargetRef.NodeSet != "data" || a.Spec.Mode != "DryRun" {
		t.Fatal("/scale changed wrong fields")
	}
	es := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "elasticsearch.k8s.elastic.co/v1", "kind": "Elasticsearch", "metadata": map[string]any{"name": "es", "namespace": "test", "annotations": map[string]any{controller.OwnerAnnotation: "scale", controller.NodeSetAnnotation: "data", controller.OwnersAnnotation: `{"data":"scale","other":"scale-other"}`}}, "spec": map[string]any{"nodeSets": []any{map[string]any{"name": "masters", "count": int64(1), "config": map[string]any{"node.roles": []any{"master"}}}, map[string]any{"name": "data", "count": int64(2), "config": map[string]any{"node.roles": []any{"data_hot"}}}, map[string]any{"name": "other", "count": int64(1), "config": map[string]any{"node.roles": []any{"data_hot"}}}}}}}
	if e = c.Create(ctx, es); e != nil {
		t.Fatal(e)
	}
	_ = unstructured.SetNestedField(es.Object, "Ready", "status", "phase")
	_ = unstructured.SetNestedField(es.Object, es.GetGeneration(), "status", "observedGeneration")
	if e = c.Status().Update(ctx, es); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"masters-0", "data-0", "data-1", "other-0"} {
		set := "data"
		if name == "masters-0" {
			set = "masters"
		}
		if name == "other-0" {
			set = "other"
		}
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test", Labels: map[string]string{"elasticsearch.k8s.elastic.co/cluster-name": "es", "elasticsearch.k8s.elastic.co/statefulset-name": "es-es-" + set}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "es", Image: "test"}}}}
		if e = c.Create(ctx, pod); e != nil {
			t.Fatal(e)
		}
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		if e = c.Status().Update(ctx, pod); e != nil {
			t.Fatal(e)
		}
	}
	r := &controller.Reconciler{Client: c, Reader: c, Safety: healthy{}}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}
	if _, e = r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	}
	if e = c.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, count, _, _ := controller.Target(es, "data")
	if count != 2 || es.GetAnnotations()[controller.LastScaleAnnotation] != "" {
		t.Fatal("dry run persisted mutation")
	}
	if e = c.Get(ctx, client.ObjectKeyFromObject(a), a); e != nil {
		t.Fatal(e)
	}
	if a.Status.Conditions[0].Reason != "DryRun" {
		t.Fatalf("unexpected status: %+v", a.Status)
	}
	if e = c.SubResource("scale").Get(ctx, a, scale); e != nil {
		t.Fatal(e)
	}
	if scale.Status.Replicas != 2 || scale.Spec.Replicas != 8 || scale.Status.Selector != "elasticsearch.k8s.elastic.co/cluster-name=es,elasticsearch.k8s.elastic.co/statefulset-name=es-es-data" {
		t.Fatalf("incorrect observed scale: %+v", scale)
	}
	invalid := a.DeepCopy()
	invalid.Spec.TargetRef.NodeSet = "masters"
	if e = c.Update(ctx, invalid); e == nil {
		t.Fatal("accepted immutable target change")
	}
	invalid = a.DeepCopy()
	invalid.Spec.Safety.BlockScaleDown = false
	if e = c.Update(ctx, invalid); e == nil {
		t.Fatal("accepted scale down enablement")
	}
	a.Spec.Mode = "Auto"
	if e = c.Update(ctx, a); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	}
	if e = c.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, count, _, _ = controller.Target(es, "data")
	if count != 4 {
		t.Fatalf("expected 4 got %d", count)
	}
	// Restart with a fresh Reconciler: ECK observed generation/pod counts prevent another increase.
	restarted := &controller.Reconciler{Client: c, Reader: c, Safety: healthy{}}
	if _, e = restarted.Reconcile(ctx, req); e != nil {
		t.Fatal(e)
	}
	if e = c.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, count, _, _ = controller.Target(es, "data")
	if count != 4 {
		t.Fatal("scaled twice while pending")
	}
	if e = c.SubResource("scale").Get(ctx, a, scale); e != nil {
		t.Fatal(e)
	}
	if scale.Status.Replicas != 2 || scale.Spec.Replicas != 8 {
		t.Fatalf("/scale must report observed Pods while ECK count=4: %+v", scale)
	}
	// Another authorized CR cannot start a second operation on this Elasticsearch.
	other := a.DeepCopy()
	other.ObjectMeta = metav1.ObjectMeta{Name: "scale-other", Namespace: "test"}
	other.Status = api.ElasticsearchQueueAutoscalerStatus{}
	other.Spec.TargetRef.NodeSet = "other"
	other.Spec.RequestedReplicas = 3
	other.Spec.Mode = "Auto"
	if e = c.Create(ctx, other); e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(other)}); e != nil {
		t.Fatal(e)
	}
	if e = c.Get(ctx, client.ObjectKeyFromObject(other), other); e != nil {
		t.Fatal(e)
	}
	if other.Status.Conditions[0].Reason != "PendingScale" {
		t.Fatalf("other autoscaler: %+v", other.Status)
	}
	if e = c.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	_, otherCount, _, _ := controller.Target(es, "other")
	if otherCount != 1 {
		t.Fatal("second NodeSet scaled before first converged")
	}
	// Two concurrent patches with the same resourceVersion: exactly one can commit.
	p, e := controller.ScalePatch(es, 1, 4, 5, time.Now(), 60)
	if e != nil {
		t.Fatal(e)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := es.DeepCopy()
			if c.Patch(ctx, target, client.RawPatch(types.JSONPatchType, p)) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("CAS successes=%d", successes.Load())
	}
	if e = c.Get(ctx, client.ObjectKeyFromObject(es), es); e != nil {
		t.Fatal(e)
	}
	sets, _, _ := unstructured.NestedSlice(es.Object, "spec", "nodeSets")
	if sets[0].(map[string]any)["count"] != int64(1) {
		t.Fatal("masters modified")
	}
}
