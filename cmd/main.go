package main

import (
	"flag"
	"fmt"
	api "github.com/thallesdaniell/elasticsearch-queue-autoscaler/api/v1alpha1"
	"github.com/thallesdaniell/elasticsearch-queue-autoscaler/internal/controller"
	"github.com/thallesdaniell/elasticsearch-queue-autoscaler/internal/safety"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	_ "time/tzdata"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	var namespace, ca, key string
	flag.StringVar(&namespace, "namespace", "", "single namespace to manage (required)")
	flag.StringVar(&ca, "es-ca-file", "/etc/elasticsearch/ca.crt", "Elasticsearch CA bundle")
	flag.StringVar(&key, "es-api-key-file", "/etc/elasticsearch/api-key", "read-only Elasticsearch API key")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	if namespace == "" {
		return fmt.Errorf("--namespace is required")
	}
	scheme := runtime.NewScheme()
	if e := clientgoscheme.AddToScheme(scheme); e != nil {
		return e
	}
	if e := api.AddToScheme(scheme); e != nil {
		return e
	}
	m, e := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{Scheme: scheme, Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}}, LeaderElection: true, LeaderElectionID: "elasticsearch-queue-autoscaler", LeaderElectionNamespace: namespace, Metrics: metricsserver.Options{BindAddress: ":8080"}, HealthProbeBindAddress: ":8081"})
	if e != nil {
		return e
	}
	checker, e := safety.New(ca, key)
	if e != nil {
		return e
	}
	r := &controller.Reconciler{Client: m.GetClient(), Reader: m.GetAPIReader(), Safety: checker, Recorder: m.GetEventRecorderFor("elasticsearch-queue-autoscaler")}
	if e = r.SetupWithManager(m); e != nil {
		return e
	}
	if e = m.AddHealthzCheck("healthz", healthz.Ping); e != nil {
		return e
	}
	if e = m.AddReadyzCheck("readyz", healthz.Ping); e != nil {
		return e
	}
	return m.Start(ctrl.SetupSignalHandler())
}
