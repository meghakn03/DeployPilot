package controller

import (
	"context"
	"fmt"
	"sync"

	api "github.com/deploypilot/deploypilot/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	client.Client
	SchemeRef *runtime.Scheme
}

var requestCounts = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "deploypilot_deployment_requests", Help: "Current deployment requests by bounded lifecycle state."}, []string{"state"})
var registerMetrics sync.Once

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	registerMetrics.Do(func() {
		metrics.Registry.MustRegister(requestCounts)
		requestCounts.WithLabelValues("active").Set(0)
		requestCounts.WithLabelValues("failed").Set(0)
	})
	d := &api.DeploymentRequest{}
	if err := r.Get(ctx, req.NamespacedName, d); err != nil {
		r.refreshRequestMetrics(ctx)
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	port := d.Spec.Port
	if port == 0 {
		port = 8080
	}
	labels := map[string]string{"app.kubernetes.io/name": d.Name, "app.kubernetes.io/managed-by": "deploypilot"}
	resources := corev1.ResourceRequirements{}
	resources.Requests = quantityMap(d.Spec.Resources.Requests)
	resources.Limits = quantityMap(d.Spec.Resources.Limits)
	c := corev1.Container{Name: d.Name, Image: d.Spec.Image, Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: port}}, Resources: resources}
	for _, e := range d.Spec.Env {
		if e.SecretName != "" {
			c.Env = append(c.Env, corev1.EnvVar{Name: e.Name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: e.SecretName}, Key: e.SecretKey}}})
		} else {
			c.Env = append(c.Env, corev1.EnvVar{Name: e.Name, Value: e.Value})
		}
	}
	if p := d.Spec.ReadinessProbe; p != nil {
		c.ReadinessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: p.Path, Port: intstr.FromInt32(port)}}, InitialDelaySeconds: p.InitialDelaySeconds, PeriodSeconds: p.PeriodSeconds}
	}
	if p := d.Spec.LivenessProbe; p != nil {
		c.LivenessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: p.Path, Port: intstr.FromInt32(port)}}, InitialDelaySeconds: p.InitialDelaySeconds, PeriodSeconds: p.PeriodSeconds}
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if err := controllerutil.SetControllerReference(d, dep, r.Scheme()); err != nil {
			return err
		}
		dep.Spec.Replicas = &desired
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		dep.Spec.Template = corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{Containers: []corev1.Container{c}}}
		return nil
	})
	if err != nil {
		r.setFailure(ctx, d, "WorkloadApplyFailed", err.Error())
		return reconcile.Result{}, err
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(d, svc, r.Scheme()); err != nil {
			return err
		}
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: port, TargetPort: intstr.FromInt32(port)}}
		return nil
	})
	if err != nil {
		r.setFailure(ctx, d, "ServiceApplyFailed", err.Error())
		return reconcile.Result{}, err
	}
	failedReason, failedMessage := r.podFailure(ctx, d, labels)
	if d.Status.ObservedGeneration != d.Generation || d.Status.ReadyReplicas != dep.Status.ReadyReplicas || d.Status.DesiredReplicas != desired || statusConditionChanged(d, dep.Status.ReadyReplicas, desired, failedReason, failedMessage) {
		d.Status.ObservedGeneration = d.Generation
		d.Status.ReadyReplicas = dep.Status.ReadyReplicas
		d.Status.DesiredReplicas = desired
		d.Status.DeploymentName = d.Name
		d.Status.ServiceName = d.Name
		condition := "Progressing"
		status := metav1.ConditionFalse
		reason := "ReplicasNotReady"
		msg := fmt.Sprintf("%d of %d replicas are ready", dep.Status.ReadyReplicas, desired)
		if failedReason != "" {
			condition = "Failed"
			reason = failedReason
			msg = failedMessage
		} else if dep.Status.ReadyReplicas >= desired {
			condition = "Available"
			status = metav1.ConditionTrue
			reason = "MinimumReplicasAvailable"
			msg = "all desired replicas are ready"
		}
		d.Status.Conditions = []api.DeploymentCondition{{Type: condition, Status: status, Reason: reason, Message: msg, LastTransitionTime: metav1.Now()}}
		if err := r.Status().Update(ctx, d); err != nil && !errors.IsConflict(err) {
			return reconcile.Result{}, err
		}
	}
	r.refreshRequestMetrics(ctx)
	return reconcile.Result{}, nil
}

func (r *Reconciler) refreshRequestMetrics(ctx context.Context) {
	list := &api.DeploymentRequestList{}
	if err := r.List(ctx, list); err != nil {
		return
	}
	active, failed := 0, 0
	for _, d := range list.Items {
		isFailed := false
		for _, c := range d.Status.Conditions {
			if c.Type == "Failed" && c.Status == metav1.ConditionTrue {
				isFailed = true
				break
			}
		}
		if isFailed {
			failed++
		} else {
			active++
		}
	}
	requestCounts.WithLabelValues("active").Set(float64(active))
	requestCounts.WithLabelValues("failed").Set(float64(failed))
}

func (r *Reconciler) podFailure(ctx context.Context, d *api.DeploymentRequest, labels map[string]string) (string, string) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(d.Namespace), client.MatchingLabels(labels)); err != nil {
		return "", ""
	}
	for _, p := range pods.Items {
		for _, s := range p.Status.ContainerStatuses {
			if s.State.Waiting != nil && (s.State.Waiting.Reason == "ImagePullBackOff" || s.State.Waiting.Reason == "ErrImagePull" || s.State.Waiting.Reason == "CreateContainerConfigError" || s.State.Waiting.Reason == "CrashLoopBackOff") {
				return s.State.Waiting.Reason, s.State.Waiting.Message
			}
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
				return c.Reason, c.Message
			}
		}
	}
	return "", ""
}

func statusConditionChanged(d *api.DeploymentRequest, ready, desired int32, reason, message string) bool {
	wantType, wantReason, wantMessage := "Progressing", "ReplicasNotReady", fmt.Sprintf("%d of %d replicas are ready", ready, desired)
	wantStatus := metav1.ConditionFalse
	if reason != "" {
		wantType, wantReason, wantMessage = "Failed", reason, message
		wantStatus = metav1.ConditionTrue
	} else if ready >= desired {
		wantType, wantReason, wantMessage = "Available", "MinimumReplicasAvailable", "all desired replicas are ready"
		wantStatus = metav1.ConditionTrue
	}
	for _, c := range d.Status.Conditions {
		if c.Type == wantType && c.Reason == wantReason && c.Message == wantMessage && c.Status == wantStatus {
			return false
		}
	}
	return true
}
func (r *Reconciler) setFailure(ctx context.Context, d *api.DeploymentRequest, reason, message string) {
	d.Status.ObservedGeneration = d.Generation
	d.Status.Conditions = []api.DeploymentCondition{{Type: "Failed", Status: metav1.ConditionTrue, Reason: reason, Message: message, LastTransitionTime: metav1.Now()}}
	if err := r.Status().Update(ctx, d); err != nil && !apierrors.IsConflict(err) {
		return
	}
}
func (r *Reconciler) Scheme() *runtime.Scheme { return r.SchemeRef }
func quantityMap(in map[string]string) corev1.ResourceList {
	if len(in) == 0 {
		return nil
	}
	out := corev1.ResourceList{}
	for k, v := range in {
		if q, e := resource.ParseQuantity(v); e == nil {
			out[corev1.ResourceName(k)] = q
		}
	}
	return out
}
