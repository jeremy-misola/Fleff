# WebApp Operator: Cloud-Native WebApp PaaS

[![Go Report Card](https://goreportcard.com/badge/github.com/jeremy-misola/WebApp-Operator)](https://goreportcard.com/report/github.com/jeremy-misola/WebApp-Operator)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Kubernetes](https://img.shields.io/badge/kubernetes-%23326ce5.svg?style=flat&logo=kubernetes&logoColor=white)](https://kubernetes.io)

 A specialized control plane extension designed to implement Platform-as-a-Service (PaaS) capabilities directly within a Kubernetes cluster. Developed using the **Go Operator SDK**, it acts as an automated "Platform Engineer," constantly monitoring the cluster to ensure application infrastructure perfectly matches the developer's intent.

## 💡 The Problem
In modern Kubernetes environments, deploying a production-ready application requires managing a fragmented stack:
1. **Workloads:** A `Deployment` with security contexts and health probes.
2. **Networking:** A `Service` for internal discovery.
3. **Ingress/Edge:** An `HTTPRoute` (**Gateway API**) for external traffic and subdomains.
4. **Automation:** DNS records via **ExternalDNS** and TLS via **Cert-Manager**.

This is error-prone and causes "YAML fatigue." **Kleff Operator** abstracts this entire stack into a single, 10-line high-level Custom Resource (CR).

## ✨ Features
- **Modern Networking:** Native integration with the **Kubernetes Gateway API** (Envoy Gateway) instead of legacy Ingress.
- **Dynamic Subdomains:** Automatically generates and manages unique subdomains (e.g., `[uuid].kleff.io`).
- **Self-Healing:** Automatically detects configuration drift. If a child Deployment or Service is deleted, the operator recreates it instantly.
- **Enterprise Ready:** Built-in support for **Azure Container Registry (ACR)** authentication, read-only root filesystems, and Prometheus metrics.
- **Status Observability:** Provides real-time feedback via CR Status conditions (Available, Progressing, Degraded).

---

## How It Works
The operator implements a **Reconciliation Loop** that watches for `WebApp` resources. For every CR, it intelligently orchestrates:
- **Deployment:** Configured with `LivenessProbes` and `acr-creds`.
- **Service:** A `ClusterIP` service mapping to your application port.
- **HTTPRoute:** Automated routing rules for Envoy Gateway with ExternalDNS annotations for Cloudflare/DNS automation.

---

## Getting Started

### Prerequisites
- **Go** v1.24.6+
- **Kubernetes** v1.30+ cluster (e.g., Kind, Minikube, or AKS)
- **Envoy Gateway** installed in the cluster (for routing)
- **Docker** installed locally for image builds

### Installation & Deployment
1. **Clone the project**
   ```bash
   git clone https://github.com/your-username/kleff-operator.git
   cd kleff-operator
   ```

2. **Install CRDs**
   ```bash
   make install
   ```

3. **Deploy the Operator**
   ```bash
   # Build and push image to your registry
   make docker-build docker-push IMG=<your-registry>/webapp-operator:latest

   # Deploy to the cluster
   make deploy IMG=<your-registry>/webapp-operator:latest
   ```

### Usage Example
Apply the following YAML to deploy an application:

```yaml
apiVersion: kleff.kleff.io/v1
kind: WebApp
metadata:
  # This name is used as the UNIQUE SUBDOMAIN (e.g., b1c2-d3e4.kleff.io)
  name: b1c2-d3e4
  namespace: default
spec:
  # Human-readable name used for pod labels (display-name)
  displayName: "Production Inventory API"

  # The UUID from your build system (used as a pod label)
  containerID: "req-998877-uuid"

  # Source code reference (for documentation/metadata)
  repoURL: "https://github.com/my-org/inventory-api"
  branch: "main"

  # The container image to deploy (Required)
  image: "kleff.azurecr.io/inventory:v1.2.3"

  # Internal port the app listens on (Default is 8080)
  # The operator uses this for LivenessProbes and Service TargetPorts
  port: 3000

  # Key-Value pairs injected into the pod as Environment Variables
  envVariables:
    NODE_ENV: "production"
    DB_CONNECTION: "postgresql://user:pass@db-host:5432/db"
    LOG_LEVEL: "info"
    API_KEY: "secret-key-value"
```

```bash
kubectl apply -f app.yaml
```
Check status: `kubectl get webapps portfolio-app -o yaml`

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

## 👤 Author
**Your Name**
- LinkedIn: [Your Profile Link]
- GitHub: [Your GitHub Link]

## 📄 License
Copyright 2025. Licensed under the **Apache License, Version 2.0**. See the `LICENSE` file for details.
