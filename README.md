# Kleff Operator: Cloud-Native WebApp PaaS

[![Go Report Card](https://goreportcard.com/badge/github.com/jeremy-misola/fleff)](https://goreportcard.com/report/github.com/jeremy-misola/fleff)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Kubernetes](https://img.shields.io/badge/kubernetes-%23326ce5.svg?style=flat&logo=kubernetes&logoColor=white)](https://kubernetes.io)

A specialized control plane extension designed to implement Platform-as-a-Service (PaaS) capabilities directly within a Kubernetes cluster. Developed using the **Go Operator SDK**, it acts as an automated "Platform Engineer," constantly monitoring the cluster to ensure application infrastructure perfectly matches the developer's intent.

## 💡 The Problem
In modern Kubernetes environments, deploying a production-ready application requires managing a fragmented stack:
1. **Workloads:** A `Deployment` with security contexts and health probes.
2. **Networking:** A `Service` for internal discovery.
3. **Ingress/Edge:** An `HTTPRoute` (**Gateway API**) for external traffic and subdomains.
4. **Automation:** DNS records via **ExternalDNS** and TLS via **Cert-Manager**.
5. **Databases:** PostgreSQL clusters with credential management.

This is error-prone and causes "YAML fatigue." **Kleff Operator** abstracts this entire stack into a single, high-level Custom Resource (CR).

## ✨ Features
- **Modern Networking:** Native integration with the **Kubernetes Gateway API** (Envoy Gateway) instead of legacy Ingress.
- **Dynamic Subdomains:** Automatically generates and manages unique subdomains (e.g., `[uuid].kleff.io`).
- **Database Provisioning:** Built-in **CloudNativePG** integration for automatic PostgreSQL cluster creation with credential injection.
- **Build Integration:** Monitors **Kaniko** build jobs and waits for image builds to complete before deploying.
- **Self-Healing:** Automatically detects configuration drift. If a child Deployment, Service, or HTTPRoute is deleted, the operator recreates it instantly.
- **Enterprise Ready:** Built-in support for **Azure Container Registry (ACR)** authentication and Prometheus metrics.
- **Status Observability:** Provides real-time feedback via CR Status conditions (Available, Progressing, Building, Degraded).

---

## How It Works
The operator implements a **Reconciliation Loop** that watches for `WebApp` resources. For every CR, it intelligently orchestrates:

1. **Database (Optional):** Creates a CloudNativePG `Cluster` if `database.enabled: true`, with automatic credential secret generation.
2. **Deployment:** Configured with environment variables, database credentials (if enabled), and `acr-creds` image pull secrets.
3. **Service:** A `ClusterIP` service mapping to your application port.
4. **HTTPRoute:** Automated routing rules for Envoy Gateway with ExternalDNS annotations for DNS automation.

### Build Flow
When `buildJobName` is specified, the operator:
1. Monitors the Kaniko build job status
2. Waits for successful completion before proceeding with deployment
3. Reports `Building` status while waiting
4. Fails with `BuildFailed` if the job fails

---

## Getting Started

### Prerequisites
- **Go** v1.25.0+
- **Kubernetes** v1.30+ cluster (e.g., Kind, Minikube, or AKS)
- **Envoy Gateway** installed in the cluster (for routing)
- **CloudNativePG** operator installed (for database provisioning)
- **Docker** installed locally for image builds

### Installation & Deployment
1. **Clone the project**
   ```bash
   git clone https://github.com/jeremy-misola/fleff.git
   cd fleff
   ```

2. **Install CRDs**
   ```bash
   make install
   ```

3. **Deploy the Operator**
   ```bash
   # Build and push image to your registry
   make docker-build docker-push IMG=<your-registry>/kleff-operator:latest

   # Deploy to the cluster
   make deploy IMG=<your-registry>/kleff-operator:latest
   ```

### Usage Example

#### Basic WebApp
Apply the following YAML to deploy a simple application:

```yaml
apiVersion: kleff.kleff.io/v1
kind: WebApp
metadata:
  name: my-app
  namespace: default
spec:
  displayName: "My Production App"
  containerID: "req-998877-uuid"
  repoURL: "https://github.com/my-org/my-repo.git"
  branch: "main"
  image: "nginx:1.25.3"
  port: 8080
  envVariables:
    NODE_ENV: "production"
    LOG_LEVEL: "info"
```

#### WebApp with Database
For applications requiring a PostgreSQL database:

```yaml
apiVersion: kleff.kleff.io/v1
kind: WebApp
metadata:
  name: inventory-api
  namespace: default
spec:
  displayName: "Production Inventory API"
  containerID: "req-998877-uuid"
  repoURL: "https://github.com/my-org/inventory-api"
  branch: "main"
  image: "kleff.azurecr.io/inventory:v1.2.3"
  port: 3000

  # Database configuration
  database:
    enabled: true
    storageSize: 10        # Size in Gi
    version: "16.2"        # PostgreSQL version

  # Environment variables
  envVariables:
    NODE_ENV: "production"
    LOG_LEVEL: "info"
```

When `database.enabled: true`, the operator automatically:
- Creates a CloudNativePG cluster named `db-{containerID}`
- Injects the following environment variables from the auto-generated secret:
  - `DATABASE_HOST`
  - `DATABASE_PORT`
  - `DATABASE_USER`
  - `DATABASE_PASSWORD`
  - `DATABASE_NAME`

#### WebApp with Build Integration
To integrate with Kaniko build jobs:

```yaml
apiVersion: kleff.kleff.io/v1
kind: WebApp
metadata:
  name: built-app
  namespace: default
spec:
  displayName: "App with Build"
  containerID: "req-aabbcc-uuid"
  image: "kleff.azurecr.io/app:latest"
  port: 8080
  buildJobName: "kaniko-build-aabbcc"  # Operator waits for this job
```

```bash
kubectl apply -f app.yaml
```

Check status:
```bash
kubectl get webapps my-app -o yaml
```

---

## 🧪 Testing & Quality
Quality is baked into the operator through a multi-tier testing strategy:
- **Unit Tests:** Validate controller logic using `envtest`.
- **E2E Tests:** A full suite that spins up a **Kind** cluster, installs the operator, and validates that the Pods and Gateway routes are successfully created.

```bash
# Run Unit Tests
make test

# Run End-to-End Tests
make test-e2e
```

---

## 📦 Project Distribution

### Helm Chart
The operator is package-ready. You can find the Helm chart in `dist/chart`.
```bash
helm install kleff-operator ./dist/chart -n operator-system --create-namespace
```

### Static YAML Bundle
A consolidated installation manifest is available for quick deployments:
```bash
kubectl apply -f dist/install.yaml
```

---

## 📋 API Reference

### WebAppSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `displayName` | string | No | Human-readable name for the application |
| `containerID` | string | No | UUID from the build system (used in pod labels) |
| `repoURL` | string | No | Source code repository URL |
| `branch` | string | No | Git branch reference |
| `image` | string | Yes | Container image to deploy |
| `port` | int | No | Application port (default: 8080, range: 1-65535) |
| `database` | DatabaseConfig | No | Database configuration |
| `envVariables` | map[string]string | No | Environment variables to inject |
| `buildJobName` | string | No | Name of Kaniko build job to wait for |

### DatabaseConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable PostgreSQL cluster creation |
| `storageSize` | int | 10 | Storage size in Gi |
| `version` | string | "16.2" | PostgreSQL version |

### Status Conditions

| Type | Reason | Description |
|------|--------|-------------|
| `Available` | `Available` | WebApp is running and accessible |
| `Available` | `Progressing` | Waiting for pods to become ready |
| `Available` | `Building` | Waiting for Kaniko build to complete |
| `Available` | `BuildFailed` | Kaniko build job failed |
| `Available` | `BuildCheckFailed` | Error checking build job status |
| `Available` | `DatabaseFailed` | Database provisioning failed |
| `Available` | `DeploymentFailed` | Deployment creation/update failed |

---

## 👤 Author
**Jeremy Misola**
- GitHub: [jeremy-misola](https://github.com/jeremy-misola)

## 📄 License
Copyright 2025. Licensed under the **Apache License, Version 2.0**. See the `LICENSE` file for details.