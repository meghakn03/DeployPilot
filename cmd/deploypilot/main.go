package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	api "github.com/deploypilot/deploypilot/api/v1alpha1"
	apihttp "github.com/deploypilot/deploypilot/internal/api"
	"github.com/deploypilot/deploypilot/internal/controller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	controller_runtime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mode := env("MODE", "all")
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	mgr, err := controller_runtime.NewManager(controller_runtime.GetConfigOrDie(), controller_runtime.Options{Scheme: scheme, HealthProbeBindAddress: env("PROBE_ADDRESS", ":8081"), Metrics: metricsserver.Options{BindAddress: env("METRICS_ADDRESS", ":8080")}})
	if err != nil {
		logger.Error("create manager", "error", err)
		os.Exit(1)
	}
	if mode == "all" || mode == "controller" {
		if err := controller_runtime.NewControllerManagedBy(mgr).For(&api.DeploymentRequest{}).Owns(&appsv1.Deployment{}).Owns(&corev1.Service{}).Complete(&controller.Reconciler{Client: mgr.GetClient(), SchemeRef: scheme}); err != nil {
			logger.Error("register controller", "error", err)
			os.Exit(1)
		}
	}
	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)
	if mode == "all" || mode == "api" {
		c := apihttp.New(mgr.GetClient(), apihttp.Config{Token: os.Getenv("API_TOKEN"), Namespaces: set(os.Getenv("ALLOWED_NAMESPACES")), Registries: split(os.Getenv("ALLOWED_REGISTRIES"))}, logger)
		c.Reader = mgr.GetAPIReader()
		c.Ready = func(ctx context.Context) error { return mgr.GetAPIReader().List(ctx, &corev1.NamespaceList{}) }
		srv := &http.Server{Addr: env("API_ADDRESS", ":8082"), Handler: c.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
		go func() {
			logger.Info("API listening", "address", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("API server stopped", "error", err)
				os.Exit(1)
			}
		}()
	}
	if err := mgr.Start(controller_runtime.SetupSignalHandler()); err != nil {
		logger.Error("manager stopped", "error", err)
		os.Exit(1)
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func split(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
func set(v string) map[string]bool {
	out := map[string]bool{}
	for _, x := range split(v) {
		out[x] = true
	}
	return out
}
