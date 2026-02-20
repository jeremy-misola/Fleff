package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kleffv1 "kleff.io/api/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	// Import CloudNativePG API
	postgresv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

type WebAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kleff.kleff.io,resources=webapps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kleff.kleff.io,resources=webapps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services;secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch

func (r *WebAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	webapp := &kleffv1.WebApp{}
	var err error
	if err = r.Get(ctx, req.NamespacedName, webapp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// --- NEW: BUILD JOB CHECK ---
	if webapp.Spec.BuildJobName != "" {
		buildJob := &batchv1.Job{}
		// We look in "default" namespace because that's where your main.go creates them
		err := r.Get(ctx, client.ObjectKey{Namespace: "default", Name: webapp.Spec.BuildJobName}, buildJob)

		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				// Job might have been cleaned up by TTL, proceed with deployment
				logger.Info("Build job not found, assuming image is already available", "job", webapp.Spec.BuildJobName)
			} else {
				return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "BuildCheckFailed", err.Error())
			}
		} else {
			// Check if job is still running
			if buildJob.Status.Succeeded == 0 {
				// Check if it failed
				if buildJob.Status.Failed > 0 {
					return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "BuildFailed", "The Kaniko build job failed. Check build logs.")
				}
				// Still building
				logger.Info("Build job still in progress, waiting...", "job", buildJob.Name)
				return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "Building", "Waiting for Kaniko build to complete...", 30*time.Second)
			}
			logger.Info("Build job completed successfully, proceeding to deployment", "job", buildJob.Name)
		}
	}
	// --- END BUILD JOB CHECK ---

	// Define standard labels
	labels := map[string]string{
		"app":          webapp.Name,
		"container-id": webapp.Spec.ContainerID,
		"controller":   "webapp",
	}

	// --- 1. HANDLE CLOUDNATIVEPG CLUSTER ---
	// Unique Name based on ContainerID to avoid collisions in the same namespace
	dbClusterName := fmt.Sprintf("db-%s", webapp.Spec.ContainerID)

	if webapp.Spec.Database.Enabled {
		dbCluster := &postgresv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dbClusterName,
				Namespace: webapp.Namespace,
			},
		}

		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, dbCluster, func() error {
			dbCluster.Labels = labels

			// Using the version from CRD
			dbCluster.Spec.ImageName = fmt.Sprintf("ghcr.io/cloudnative-pg/postgresql:%s", webapp.Spec.Database.Version)

			// Storage size from CRD
			storageSize := fmt.Sprintf("%dGi", webapp.Spec.Database.StorageSize)
			if dbCluster.Spec.StorageConfiguration == (postgresv1.StorageConfiguration{}) {
				dbCluster.Spec.StorageConfiguration = postgresv1.StorageConfiguration{}
			}
			dbCluster.Spec.StorageConfiguration.Size = storageSize

			// Set number of instances
			instances := 1
			dbCluster.Spec.Instances = instances

			return controllerutil.SetControllerReference(webapp, dbCluster, r.Scheme)
		})
		if err != nil {
			logger.Error(err, "Failed to reconcile Postgres Cluster")
			return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "DatabaseFailed", err.Error())
		}
	}

	// --- 2. SYNC DEPLOYMENT ---
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name,
			Namespace: webapp.Namespace,
		},
	}

	// Check for Database Secret BEFORE the CreateOrUpdate block
	var dbSecretName string
	if webapp.Spec.Database.Enabled {
		dbSecretName = dbClusterName + "-app"
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: webapp.Namespace, Name: dbSecretName}, secret)
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				logger.Info("Database secret not ready yet, requeueing", "secret", dbSecretName)
				// Requeue after 5 seconds to give CNPG time to create the secret
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
			return r.updateStatus(ctx, webapp, metav1.ConditionFalse, "SecretError", err.Error())
		}
	}


	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Labels = labels
		if deployment.CreationTimestamp.IsZero() {
			deployment.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": webapp.Name},
			}
		}

		replicas := int32(1)
		deployment.Spec.Replicas = &replicas
		deployment.Spec.Template.ObjectMeta.Labels = labels
		deployment.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "acr-creds"}}

		enableServiceLinks := false
		deployment.Spec.Template.Spec.EnableServiceLinks = &enableServiceLinks

		// Gather Environment Variables
		var envVars []corev1.EnvVar
		for k, v := range webapp.Spec.EnvVariables {
			envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
		}

		// Inject DB Secrets if enabled
		// CNPG naming convention for the app secret is: <cluster-name>-app
		if webapp.Spec.Database.Enabled {
			dbSecretName := dbClusterName + "-app"

			dbKeys := map[string]string{
				"DATABASE_HOST":     "host",
				"DATABASE_PORT":     "port",
				"DATABASE_USER":     "user",
				"DATABASE_PASSWORD": "password",
				"DATABASE_NAME":     "dbname",
			}

			for envName, secretKey := range dbKeys {
				envVars = append(envVars, corev1.EnvVar{
					Name: envName,
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: dbSecretName},
							Key:                  secretKey,
						},
					},
				})
			}
		}

		// Sort Envs to prevent "Permadiff" (Go map iteration is random)
		sort.Slice(envVars, func(i, j int) bool { return envVars[i].Name < envVars[j].Name })

		deployment.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:            "app",
			Image:           webapp.Spec.Image,
			ImagePullPolicy: corev1.PullAlways,
			Env:             envVars,
			Ports: []corev1.ContainerPort{{
				ContainerPort: int32(webapp.Spec.Port),
			}},
		}}

		return controllerutil.SetControllerReference(webapp, deployment, r.Scheme)
	})

	if err != nil {
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

func (r *WebAppReconciler) updateStatus(ctx context.Context, webapp *kleffv1.WebApp, status metav1.ConditionStatus, reason, message string, requeueAfter ...time.Duration) (ctrl.Result, error) {
	currentCond := meta.FindStatusCondition(webapp.Status.Conditions, "Available")

	if currentCond != nil &&
		currentCond.Status == status &&
		currentCond.Reason == reason &&
		currentCond.Message == message {
		if len(requeueAfter) > 0 {
			return ctrl.Result{RequeueAfter: requeueAfter[0]}, nil
		}
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

	if len(requeueAfter) > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter[0]}, nil
	}
	return ctrl.Result{}, nil
}

func (r *WebAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kleffv1.WebApp{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Owns(&postgresv1.Cluster{}).
		Complete(r)
}
