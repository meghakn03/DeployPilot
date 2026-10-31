package api

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	api "github.com/deploypilot/deploypilot/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed web/*
var webAssets embed.FS

type Config struct {
	Token        string
	Namespaces   map[string]bool
	Registries   []string
	MaxReplicas  int32
	MaxBodyBytes int64
}
type Server struct {
	Client   client.Client
	Reader   client.Reader
	Config   Config
	Logger   *slog.Logger
	Requests *prometheus.CounterVec
	Latency  *prometheus.HistogramVec
	Ready    func(context.Context) error
}
type Error struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields,omitempty"`
	} `json:"error"`
}

var dnsName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func New(c client.Client, cfg Config, logger *slog.Logger) *Server {
	if cfg.MaxReplicas == 0 {
		cfg.MaxReplicas = 20
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = 1 << 20
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{Client: c, Reader: c, Config: cfg, Logger: logger, Requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "deploypilot_api_requests_total", Help: "API requests by route and status."}, []string{"route", "method", "status"}), Latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "deploypilot_api_request_duration_seconds", Help: "API request duration.", Buckets: prometheus.DefBuckets}, []string{"route", "method"})}
	return s
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(webAssets, "web")
	mux.Handle("/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("/readyz", s.ready)
	reg := prometheus.NewRegistry()
	reg.MustRegister(s.Requests, s.Latency)
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.Handle("/v1/deployments", s.observe("list_create", http.HandlerFunc(s.collection)))
	mux.Handle("/v1/deployments/", s.observe("item", http.HandlerFunc(s.item)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			s.auth(mux).ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Config.Token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if len(got) != len(s.Config.Token) || subtle.ConstantTimeCompare([]byte(got), []byte(s.Config.Token)) != 1 {
				fail(w, 401, "unauthorized", "valid bearer token required", nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) observe(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		s.Requests.WithLabelValues(route, r.Method, fmt.Sprint(rw.status)).Inc()
		s.Latency.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.Ready(ctx); err != nil {
			fail(w, 503, "not_ready", "Kubernetes API is unavailable", nil)
			return
		}
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}
func (s *Server) collection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.create(w, r)
	case http.MethodGet:
		s.list(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		fail(w, 405, "method_not_allowed", "method not allowed", nil)
	}
}
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.Config.MaxBodyBytes)
	var in struct {
		Name           string           `json:"name"`
		Namespace      string           `json:"namespace"`
		Image          string           `json:"image"`
		Replicas       *int32           `json:"replicas,omitempty"`
		Port           int32            `json:"port,omitempty"`
		Resources      api.ResourceSpec `json:"resources,omitempty"`
		ReadinessProbe *api.ProbeSpec   `json:"readinessProbe,omitempty"`
		LivenessProbe  *api.ProbeSpec   `json:"livenessProbe,omitempty"`
		Env            []api.EnvVar     `json:"env,omitempty"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, 400, "invalid_json", err.Error(), nil)
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		fail(w, 400, "invalid_json", "request must contain one JSON object", nil)
		return
	}
	fields := map[string]string{}
	if !dnsName.MatchString(in.Name) || len(in.Name) > 63 {
		fields["name"] = "must be a lowercase DNS label"
	}
	if in.Namespace == "" {
		fields["namespace"] = "is required"
	} else if len(s.Config.Namespaces) > 0 && !s.Config.Namespaces[in.Namespace] {
		fields["namespace"] = "namespace is not allowed"
	}
	allowed := false
	for _, prefix := range s.Config.Registries {
		if strings.HasPrefix(in.Image, prefix) {
			allowed = true
			break
		}
	}
	if len(s.Config.Registries) > 0 && !allowed {
		fields["image"] = "registry is not allowed"
	}
	if in.Image == "" {
		fields["image"] = "is required"
	}
	n := int32(1)
	if in.Replicas != nil {
		n = *in.Replicas
	}
	if n < 1 || n > s.Config.MaxReplicas {
		fields["replicas"] = fmt.Sprintf("must be between 1 and %d", s.Config.MaxReplicas)
	}
	if in.Port == 0 {
		in.Port = 8080
	}
	if in.Port < 1 || in.Port > 65535 {
		fields["port"] = "must be between 1 and 65535"
	}
	validateResources(in.Resources, fields)
	for _, probe := range []*api.ProbeSpec{in.ReadinessProbe, in.LivenessProbe} {
		if probe != nil && (probe.Path == "" || !strings.HasPrefix(probe.Path, "/") || len(probe.Path) > 256 || probe.InitialDelaySeconds < 0 || probe.PeriodSeconds < 0) {
			fields["probe"] = "path must start with / and probe timings must be non-negative"
		}
	}
	for _, e := range in.Env {
		if !envName.MatchString(e.Name) {
			fields["env"] = "environment variable names must be valid"
		}
		if e.SecretName == "" && e.SecretKey != "" || e.SecretName != "" && (e.SecretKey == "" || e.Value != "") {
			fields["env"] = "provide either a literal value or a secret name and key"
		}
	}
	if len(fields) > 0 {
		fail(w, 422, "validation_failed", "request validation failed", fields)
		return
	}
	d := &api.DeploymentRequest{TypeMeta: metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "DeploymentRequest"}, ObjectMeta: metav1.ObjectMeta{Name: in.Name, Namespace: in.Namespace}, Spec: api.DeploymentRequestSpec{Image: in.Image, Replicas: &n, Port: in.Port, Resources: in.Resources, ReadinessProbe: in.ReadinessProbe, LivenessProbe: in.LivenessProbe, Env: in.Env}}
	if err := s.Client.Create(r.Context(), d); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			fail(w, 409, "already_exists", "a deployment request with this name already exists", nil)
		} else {
			s.Logger.Error("persist deployment request", "namespace", in.Namespace, "name", in.Name, "error", err)
			fail(w, 503, "persistence_failed", "could not persist deployment request", nil)
		}
		return
	}
	w.Header().Set("Location", "/v1/deployments/"+in.Name)
	writeJSON(w, 202, d)
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	limit := int64(100)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			fail(w, 400, "invalid_limit", "limit must be an integer between 1 and 500", nil)
			return
		}
		limit = parsed
		if limit < 1 || limit > 500 {
			fail(w, 400, "invalid_limit", "limit must be between 1 and 500", nil)
			return
		}
	}
	opts := []client.ListOption{client.Limit(limit)}
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		if len(s.Config.Namespaces) > 0 && !s.Config.Namespaces[ns] {
			fail(w, 403, "forbidden", "namespace is not allowed", nil)
			return
		}
		opts = append(opts, client.InNamespace(ns))
	} else if len(s.Config.Namespaces) > 0 {
		if len(s.Config.Namespaces) == 1 {
			for ns := range s.Config.Namespaces {
				opts = append(opts, client.InNamespace(ns))
			}
		} else {
			fail(w, 400, "namespace_required", "namespace query parameter is required when multiple namespaces are allowed", nil)
			return
		}
	}
	if cont := r.URL.Query().Get("continue"); cont != "" {
		opts = append(opts, client.Continue(cont))
	}
	out := &api.DeploymentRequestList{}
	if err := s.Reader.List(r.Context(), out, opts...); err != nil {
		fail(w, 503, "list_failed", "could not list deployment requests", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"items": out.Items, "continue": out.Continue})
}

func validateResources(spec api.ResourceSpec, fields map[string]string) {
	for kind, values := range map[string]map[string]string{"requests": spec.Requests, "limits": spec.Limits} {
		for name, value := range values {
			if name != "cpu" && name != "memory" {
				fields["resources"] = "only cpu and memory are supported"
				continue
			}
			q, err := resource.ParseQuantity(value)
			if err != nil || q.Sign() < 0 {
				fields["resources"] = "resource quantities must be valid and non-negative"
				continue
			}
			maximum := resource.MustParse("16Gi")
			if kind == "requests" && name == "cpu" {
				maximum = resource.MustParse("8")
			}
			if kind == "limits" && name == "cpu" {
				maximum = resource.MustParse("16")
			}
			if kind == "limits" && name == "memory" {
				maximum = resource.MustParse("32Gi")
			}
			if q.Cmp(maximum) > 0 {
				fields["resources"] = fmt.Sprintf("%s %s exceeds the %s maximum", kind, name, maximum.String())
			}
		}
	}
	for _, name := range []string{"cpu", "memory"} {
		req, hasReq := spec.Requests[name]
		lim, hasLim := spec.Limits[name]
		if hasReq && hasLim {
			rq, e1 := resource.ParseQuantity(req)
			lq, e2 := resource.ParseQuantity(lim)
			if e1 == nil && e2 == nil && lq.Cmp(rq) < 0 {
				fields["resources"] = fmt.Sprintf("%s limit must be at least its request", name)
			}
		}
	}
}
func (s *Server) item(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
	if name == "" || strings.Contains(name, "/") {
		fail(w, 404, "not_found", "deployment request not found", nil)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, DELETE")
		fail(w, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		fail(w, 400, "namespace_required", "namespace query parameter is required", nil)
		return
	}
	if len(s.Config.Namespaces) > 0 && !s.Config.Namespaces[ns] {
		fail(w, 403, "forbidden", "namespace is not allowed", nil)
		return
	}
	d := &api.DeploymentRequest{}
	if r.Method == http.MethodGet {
		if err := s.Reader.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, d); err != nil {
			fail(w, 404, "not_found", "deployment request not found", nil)
			return
		}
		writeJSON(w, 200, d)
		return
	}
	if err := s.Client.Delete(r.Context(), &api.DeploymentRequest{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}); err != nil {
		fail(w, 404, "not_found", "deployment request not found", nil)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, msg string, fields map[string]string) {
	e := Error{}
	e.Error.Code = code
	e.Error.Message = msg
	e.Error.Fields = fields
	writeJSON(w, status, e)
}
