package controller

import (
	"context"
	"fmt"
	"regexp" 
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	// Import Gateway API types
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	kleffv1 "kleff.io/api/v1"
)

type WebAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=kleff.kleff.io,resources=webapps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kleff.kleff.io,resources=webapps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
func (r *WebAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch the WebApp CR
	webapp := &kleffv1.WebApp{}
	err := r.Get(ctx, req.NamespacedName, webapp)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// --- DEFINE LABELS ---
	// "app" is the UUID (webapp.Name). 
	// We add "display-name" for human observability via kubectl.
	labels := map[string]string{
		"app":          webapp.Name, // This is the UUID
		"container-id": webapp.Spec.ContainerID,
		"controller":   "webapp",
	}
	if webapp.Spec.DisplayName != "" {
		// Sanitize display name for label safety (max 63 chars, alphanumeric)
		safeDisplayName := regexp.MustCompile(`[^a-z0-9A-Z._-]`).ReplaceAllString(webapp.Spec.DisplayName, "-")
		labels["display-name"] = safeDisplayName
	}

	// 2. Sync Deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name, // UUID
			Namespace: webapp.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Labels = labels

		// Selector is immutable after creation, so we set it only if new
		if deployment.CreationTimestamp.IsZero() {
			deployment.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": webapp.Name},
			}
		}

		replicas := int32(1)
		deployment.Spec.Replicas = &replicas

		// Pod Template
		if deployment.Spec.Template.ObjectMeta.Labels == nil {
			deployment.Spec.Template.ObjectMeta.Labels = make(map[string]string)
		}
		for k, v := range labels {
			deployment.Spec.Template.ObjectMeta.Labels[k] = v
		}

		deployment.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: "acr-creds"},
		}

		// Environment Variables
		var envVars []corev1.EnvVar
		for key, value := range webapp.Spec.EnvVariables {
			envVars = append(envVars, corev1.EnvVar{Name: key, Value: value})
		}

		deployment.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:            "app",
			Image:           webapp.Spec.Image,
			ImagePullPolicy: corev1.PullAlways,
			Env:             envVars,
			Ports: []corev1.ContainerPort{{
				Name:          "http",
				ContainerPort: int32(webapp.Spec.Port),
				Protocol:      corev1.ProtocolTCP,
			}},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(webapp.Spec.Port)},
				},
			},
		}}

		return controllerutil.SetControllerReference(webapp, deployment, r.Scheme)
	})

	if err != nil {
		logger.Error(err, "Failed to reconcile Deployment")
		return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "DeploymentFailed", err.Error())
	}

	// 3. Sync Service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name, // UUID
			Namespace: webapp.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		service.Labels = labels
		service.Spec.Selector = map[string]string{"app": webapp.Name}
		service.Spec.Type = corev1.ServiceTypeClusterIP
		service.Spec.Ports = []corev1.ServicePort{{
			Name:       "http",
			Port:       80,
			TargetPort: intstr.FromInt(webapp.Spec.Port),
			Protocol:   corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(webapp, service, r.Scheme)
	})

	if err != nil {
		return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "ServiceFailed", err.Error())
	}

	// 4. Sync HTTPRoute (Envoy Gateway)
	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name + "-route", // UUID-route
			Namespace: webapp.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, httpRoute, func() error {
		if httpRoute.Annotations == nil {
			httpRoute.Annotations = make(map[string]string)
		}
		// ExternalDNS targets
		httpRoute.Annotations["external-dns.alpha.kubernetes.io/target"] = "66.130.187.229"
		httpRoute.Annotations["external-dns.alpha.kubernetes.io/cloudflare-proxied"] = "false"
		httpRoute.Annotations["external-dns.alpha.kubernetes.io/ttl"] = "3600"

		gwNamespace := gatewayv1.Namespace("envoy-gateway-system")
		httpRoute.Spec.CommonRouteSpec.ParentRefs = []gatewayv1.ParentReference{
			{
				Name:      "prod-web",
				Namespace: &gwNamespace,
			},
		}

		// THE SUBDOMAIN: Using webapp.Name (UUID)
		hostname := gatewayv1.Hostname(fmt.Sprintf("%s.kleff.io", webapp.Name))
		httpRoute.Spec.Hostnames = []gatewayv1.Hostname{hostname}

		// POINT TO BACKEND: Points to the Service named with the UUID
		port := gatewayv1.PortNumber(80)
		httpRoute.Spec.Rules = []gatewayv1.HTTPRouteRule{
			{
				BackendRefs: []gatewayv1.HTTPBackendRef{
					{
						BackendRef: gatewayv1.BackendRef{
							BackendObjectReference: gatewayv1.BackendObjectReference{
								Name: gatewayv1.ObjectName(webapp.Name), // UUID Service
								Port: &port,
							},
						},
					},
				},
			},
		}

		return controllerutil.SetControllerReference(webapp, httpRoute, r.Scheme)
	})

	if err != nil {
		logger.Error(err, "Failed to reconcile HTTPRoute")
		return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "HTTPRouteFailed", err.Error())
	}

	// 5. Update Status based on Deployment Readiness
	if deployment.Status.ReadyReplicas > 0 {
		msg := fmt.Sprintf("WebApp is running at http://%s.kleff.io", webapp.Name)
		return r.updateStatus(ctx, webapp, metav1.ConditionTrue, "Available", msg)
	} else {
		return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "Progressing", "Waiting for pods to be ready")
	}
}

func (r *WebAppReconciler) updateStatus(ctx context.Context, webapp *kleffv1.WebApp, status metav1.ConditionStatus, reason, message string) (ctrl.Result, error) {
	currentCond := meta.FindStatusCondition(webapp.Status.Conditions, "Available")

	if currentCond != nil &&
		currentCond.Status == status &&
		currentCond.Reason == reason &&
		currentCond.Message == message {
		return ctrl.Result{}, nil
	}

	meta.SetStatusCondition(&webapp.Status.Conditions, metav1.Condition{
		Type:               "Available",
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, webapp); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kleffv1.WebApp{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Complete(r)
}
